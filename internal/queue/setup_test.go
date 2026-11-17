package queue

import (
	"bytes"
	"flag"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/queue/types"
	"github.com/egladman/magus/libs/clikit/exit"
	magustypes "github.com/egladman/magus/types"
)

type SetupSuite struct{ suite.Suite }

func TestSetupSuite(t *testing.T) { suite.Run(t, new(SetupSuite)) }

func (s *SetupSuite) write(provider string, caps types.Capabilities) string {
	var b bytes.Buffer
	s.Require().NoError(WriteSetup(&b, provider, "main", caps))
	return b.String()
}

// A base requiring no approval is said out loud: the queue then merges what nobody
// reviewed, and adds no review rule of its own.
func (s *SetupSuite) TestWriteSetupSaysWhenTheBaseRequiresNoApproval() {
	got := s.write("local.buzz", types.Capabilities{
		StackMerge: "sequential", Methods: []types.MergeMethod{"squash"}, QueueLabel: "merge: ",
	})
	s.Equal(`# local.buzz on main: merge methods squash; stacks merge sequential; a stack queues by the label "merge: <method>"
# main requires no approval, so the queue merges a change nobody approved; it adds no review rule of its own
# no setup: --status-context is empty, or provider local.buzz reports none
`, got)
}

// The setup prints as a script: every fact a comment, every step a command or a link.
func (s *SetupSuite) TestWriteSetupPrintsTheStepsAPersonRuns() {
	got := s.write("local.buzz", types.Capabilities{
		StackMerge: "atomic", Methods: []types.MergeMethod{"squash", "merge"}, QueueLabel: "queue: ", RequiredApprovals: 2,
		Setup: &types.Setup{
			StatusContext: "merge-queue",
			Credential:    types.Integration{ID: "812", Name: "q"},
			RequiredChecks: []types.RequiredCheck{
				{Context: "merge-queue", Integration: "15368"},
				{Context: "ci gate", Events: []string{"pull_request"}},
			},
			Settings: []types.Setting{{Name: "allow_auto_merge", Value: "false", Want: "true"}},
			Steps: []types.SetupStep{
				{Title: "Allow auto-merge", Command: "gh api -X PATCH repos/acme/widgets -F allow_auto_merge=true"},
				{Title: "Install it", URL: "https://github.com/apps/q/installations/new"},
			},
		},
	})
	s.Equal(`# local.buzz on main: merge methods squash, merge; stacks merge atomic; a stack queues by the label "queue: <method>"
# main requires 2 approving reviews at the commit a review of a change's head covers
# the queue posts "merge-queue" as 812 (q)
# main requires "merge-queue" from integration 15368 (not seen reported)
# main requires "ci gate" from any source (seen on pull_request)
# allow_auto_merge is false; the queue needs true

# 1. Allow auto-merge
gh api -X PATCH repos/acme/widgets -F allow_auto_merge=true

# 2. Install it
# open https://github.com/apps/q/installations/new
`, got)
}

func (s *SetupSuite) TestRenderRerunCarriesTheFlagsGivenAndTheProvidersApp() {
	fs := flag.NewFlagSet("describe", flag.ContinueOnError)
	for _, name := range []string{"provider", "base", "status-context", "remote", "vcs", "app"} {
		fs.String(name, "", "")
	}
	s.Require().NoError(fs.Parse([]string{"--status-context", "my gate", "--remote", "origin", "--app", "unknown",
		"--base", "main", "--provider", "local.buzz"}))
	got := RenderRerun("/work tree", fs, []string{"provider", "base", "status-context", "remote", "vcs"}, "<slug>")
	s.Equal(`magus --root '/work tree' queue describe --provider local.buzz --base main --status-context 'my gate' --remote origin --app '<slug>'`, got)
}

func (s *SetupSuite) TestRequiredNamesTheFirstFlagGivenNoValue() {
	s.NoError(Required("ls", [2]string{"provider", "github"}, [2]string{"base", "main"}))
	err := Required("ls", [2]string{"provider", "github"}, [2]string{"base", ""})
	s.Require().EqualError(err, "magus queue ls: --base is required")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *SetupSuite) TestPassEnvRefusesANameTheBoxOwns() {
	env, err := PassEnv(HookEnv{}, "", "gate")
	s.Require().NoError(err)
	s.Equal(HookEnv{}, env)
	_, err = PassEnv(HookEnv{}, "A=B", "gate")
	s.Require().Error(err)
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "magus queue gate: --env")
}

func (s *SetupSuite) TestParseCommitter() {
	got, err := ParseCommitter("Merge Bot <bot@example.invalid>")
	s.Require().NoError(err)
	s.Equal(magustypes.Person{Name: "Merge Bot", Email: "bot@example.invalid"}, got)
	got, err = ParseCommitter("")
	s.Require().NoError(err)
	s.Equal(magustypes.Person{}, got)
	for _, bad := range []string{"nobody", "<bot@example.invalid>", "Bot <>", "Bot <a b@c>", "Bot <a@b"} {
		_, err := ParseCommitter(bad)
		s.Require().Error(err, bad)
	}
}
