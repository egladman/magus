package guard

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/cmdline"
)

type WaitSetSuite struct {
	suite.Suite
	root string
}

func TestWaitSetSuite(t *testing.T) {
	suite.Run(t, new(WaitSetSuite))
}

func (s *WaitSetSuite) SetupTest() {
	s.root = s.T().TempDir()
}

var backtickSpan = regexp.MustCompile("`([^`]+)`")

// waitLines hold a run each wait rule serves a remedy for.
var waitLines = map[string]string{
	"busy-wait":    "until ./magus run test .; do sleep 30; done",
	"sleep-poll":   "sleep 30; ./magus server status; ./magus run test .",
	"process-poll": "pgrep -f magus; ./magus run test .",
}

func (s *WaitSetSuite) served(line string) ShellVerdict {
	return Evaluate(Dependencies{scope: workspaceScope{root: s.root}}, line)
}

func magusSpans(text string) [][]string {
	var spans [][]string
	for _, m := range backtickSpan.FindAllStringSubmatch(text, -1) {
		argv := strings.Fields(m[1])
		if len(argv) > 0 && filepath.Base(argv[0]) == "magus" {
			spans = append(spans, argv)
		}
	}
	return spans
}

func (s *WaitSetSuite) TestTheNoticeFormIsServed() {
	s.True(servesWait(boundedRunArgv("./magus", "18m0s", []string{"./magus", "run", "test", "."})))
}

func (s *WaitSetSuite) TestAJobExecWithoutATimeoutIsNotServed() {
	s.False(servesWait([]string{"./magus", "job", "exec", "--", "./magus", "run", "test", "."}))
}

func (s *WaitSetSuite) TestATimeoutAfterTheSeparatorBelongsToTheRun() {
	s.False(servesWait([]string{"./magus", "job", "exec", "--", "./magus", "x", "--timeout", "5m"}))
}

func (s *WaitSetSuite) TestJobWaitIsNotServed() {
	s.False(servesWait([]string{"./magus", "job", "wait", "my-job"}))
}

func (s *WaitSetSuite) TestAForgeWatchIsNotServed() {
	s.False(servesWait([]string{"gh", "run", "watch"}))
}

func (s *WaitSetSuite) TestAnArgvWithNoSubcommandIsNotServed() {
	s.False(servesWait([]string{"./magus"}))
}

func (s *WaitSetSuite) TestEveryWaitLeafTheTreeDeclaresIsServed() {
	cmdline.Walk(commands.Root, func(path string, c cmdline.Command) {
		if len(c.Children) > 0 {
			return
		}
		argv := append([]string{"magus"}, strings.Fields(path)...)
		if c.Effect == cmdline.EffectWait {
			s.True(servesWait(argv), path)
		}
		for _, f := range c.Flags {
			if f.Effect == cmdline.EffectWait {
				s.True(servesWait(append(argv, "--"+f.Name, "1m")), fmt.Sprintf("%s --%s", path, f.Name))
			}
		}
	})
}

func (s *WaitSetSuite) TestTheUndeclaredFormsAreLeavesOfTheTree() {
	leaf := map[string]bool{}
	cmdline.Walk(commands.Root, func(path string, c cmdline.Command) {
		leaf[path] = len(c.Children) == 0
	})
	for _, f := range []waitForm{budgetRunForm, budgetAffectedForm, noticeForm} {
		s.True(leaf[strings.Join(f.words, " ")], f.String())
	}
}

func (s *WaitSetSuite) TestEveryServedRemedyAndQuotedSpanIsServed() {
	for rule, line := range waitLines {
		v := s.served(line)
		s.Require().Equal(rule, string(v.Rule.Name), line)
		s.Require().NotEmpty(v.Next, rule)
		for _, next := range v.Next {
			s.True(servesWait(next.Argv), "%s serves %q", rule, next.Run)
		}
		for _, span := range magusSpans(fmt.Sprintf("%s %s", v.Deny, v.Lead)) {
			s.True(servesWait(span), "%s quotes %q", rule, strings.Join(span, " "))
		}
	}
}

func (s *WaitSetSuite) TestEveryWaitDenyQuotesOnlyServedForms() {
	for name, text := range map[string]string{
		"sleep-poll":   denySleepPoll,
		"busy-wait":    denyBusyWait,
		"process-poll": denyProcessPoll,
		"slot-wait":    denySlotWait,
	} {
		spans := magusSpans(text)
		s.NotEmpty(spans, name)
		for _, span := range spans {
			s.True(servesWait(span), "%s quotes %q", name, strings.Join(span, " "))
		}
	}
}

func (s *WaitSetSuite) TestTheSlotWaitDenyNamesTheBudgetRun() {
	s.Contains(denySlotWait, fmt.Sprintf("`%s <your check>`", budgetRunForm))
	s.Equal(hint.Run.String(), budgetRunForm.String())
}

func (s *WaitSetSuite) TestEveryServedWaitLinePassesTheGuardForEveryRole() {
	ctx, roles := servedRoles(s.T())
	deps := strict(loadingWorkspace(s.T()))
	deps.Spells = testDependencies().Spells
	for rule, line := range waitLines {
		v := s.served(line)
		s.Require().NotEmpty(v.Next, rule)
		for _, role := range roles {
			call := servedAs(Request{}, role)
			call.CallBackground = true
			gradeServed(s.T(), ctx, deps, call, role.name, rule, v.Next[0].Run)
		}
	}
}
