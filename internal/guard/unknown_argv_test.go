package guard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/libs/clikit/command"
)

type UnknownMagusArgvSuite struct {
	suite.Suite
}

func TestUnknownMagusArgv(t *testing.T) {
	suite.Run(t, new(UnknownMagusArgvSuite))
}

func (s *UnknownMagusArgvSuite) judge(command string) ShellVerdict {
	deps := strict(testDependencies())
	deps.Binary, deps.BinaryVersion = "./magus", "v9.9.9"
	return Evaluate(deps, command)
}

func (s *UnknownMagusArgvSuite) refused(command string) ShellVerdict {
	v := s.judge(command)
	s.Equal(denyRuleUnknownMagusArgv, v.Rule.Name, command)
	s.NotEmpty(v.Deny, command)
	return v
}

func (s *UnknownMagusArgvSuite) passes(command string) {
	s.NotEqual(denyRuleUnknownMagusArgv, s.judge(command).Rule.Name, command)
}

func (s *UnknownMagusArgvSuite) TestUnknownTopLevelWordNamesTheNearestCommandAndTheBinary() {
	v := s.refused("./magus affcted ci")
	s.Contains(v.Deny, "`affcted`")
	s.Contains(v.Deny, "`magus affected`")
	s.Contains(v.Deny, "./magus (v9.9.9)")
}

func (s *UnknownMagusArgvSuite) TestUnknownNestedWordNamesTheNearestSibling() {
	v := s.refused("magus graph biuld")
	s.Contains(v.Deny, "`graph biuld`")
	s.Contains(v.Deny, "`magus graph build`")
}

func (s *UnknownMagusArgvSuite) TestUnbornCommandWithNothingNearNamesNone() {
	v := s.refused("magus zzzzzzzzzz")
	s.NotContains(v.Deny, "nearest")
}

func (s *UnknownMagusArgvSuite) TestUnboundFlagNamesTheLeaf() {
	v := s.refused("./magus run go-test . --budget-waitt 0")
	s.Contains(v.Deny, "`--budget-waitt`")
	s.Contains(v.Deny, "`magus run`")
}

func (s *UnknownMagusArgvSuite) TestUnboundFlagOnANestedLeaf() {
	v := s.refused("magus graph build --bogus")
	s.Contains(v.Deny, "`magus graph build`")
}

func (s *UnknownMagusArgvSuite) TestUnboundShortFlag() {
	s.refused("magus run -Z test .")
}

func (s *UnknownMagusArgvSuite) TestUnknownWordIsRefusedBehindGlobalFlags() {
	s.refused("magus --root . -o json bogus")
}

func (s *UnknownMagusArgvSuite) TestKnownCallsPass() {
	for _, command := range []string{
		"./magus run go-test . --budget-wait 0 -- ./internal/guard",
		"magus run test . --no-cache",
		"magus affected ci --plan",
		"magus -s -vv run test .",
		"magus query kind=spell go",
		"magus query output out1a2b3c --identity",
		"magus ls targets libs/foo",
		"magus describe job x",
		"magus buzz -C . -e 'x'",
		"magus help",
		"magus",
		"magus run test . -- -run TestX --count=1",
		"magus run -h",
		"magus --help",
		"magus run test . --timeout -1",
	} {
		s.passes(command)
	}
}

func (s *UnknownMagusArgvSuite) TestWordsPastTheDoubleDashAreNotRead() {
	s.passes("magus run go-test . -- bogus --bogus")
}

func (s *UnknownMagusArgvSuite) TestSilentShortFlagAfterTheTargetPasses() {
	s.passes("magus run go-test . -s")
}

func (s *UnknownMagusArgvSuite) TestTeeAndJSONLinesOutputPass() {
	s.passes("magus run lint . --tee f -o jsonl")
}

func (s *UnknownMagusArgvSuite) TestNameOutputOnAListPasses() {
	s.passes("magus ls jobs -o name")
}

func (s *UnknownMagusArgvSuite) TestJSONOutputOnAQueryPasses() {
	s.passes(`magus query "kind=x" -o json`)
}

func (s *UnknownMagusArgvSuite) TestTimeoutBeforeTheDoubleDashPasses() {
	s.passes("magus job exec x --timeout 20m -- cmd")
}

func (s *UnknownMagusArgvSuite) TestTemplateOutputPasses() {
	s.passes("magus describe job x -o template={{.ID}}")
}

func (s *UnknownMagusArgvSuite) TestBudgetWaitBeforeTheDoubleDashPasses() {
	s.passes("magus run go-test . --budget-wait 0 -- ./internal/guard")
}

func (s *UnknownMagusArgvSuite) TestOtherProgramsAreNotJudged() {
	s.passes("git bogus --bogus")
}

func (s *UnknownMagusArgvSuite) TestEveryDeclaredCommandAndFlagIsBound() {
	command.Walk(commands.Root, func(path string, c command.Command) {
		if len(c.Children) > 0 {
			return
		}
		argv := strings.Fields(path)
		if flag := firstFlag(c); flag != "" {
			argv = append(argv, "--"+flag)
		}
		got, bad := resolveUnknownArgv(argv)
		s.False(bad, "%s: %+v", path, got)
	})
}

func firstFlag(c command.Command) string {
	if len(c.Flags) == 0 {
		return ""
	}
	return c.Flags[0].Name
}
