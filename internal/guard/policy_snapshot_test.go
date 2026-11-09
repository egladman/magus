package guard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

const snapshotRules = `import "magus";

magus\guard.shell({"name": "no-frobnicate", "decision": "deny", "program": "frobnicate", "reason": "Not here."});

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    return magus\guard.deny("committed");
});
`

const snapshotShellOnly = `import "magus";

magus\guard.shell({"name": "no-frobnicate", "decision": "deny", "program": "frobnicate", "reason": "Not here."});
`

// PolicySnapshotSuite pins the guard's policy snapshot: one complete load
// answers every hook that follows until a byte it was keyed by moves.
type PolicySnapshotSuite struct {
	suite.Suite
	repo     *testkit.Repo
	loads    int
	original func(context.Context, string, types.VCSOptions) (*WorkspaceRules, error)
}

func TestPolicySnapshotSuite(t *testing.T) {
	suite.Run(t, new(PolicySnapshotSuite))
}

func (s *PolicySnapshotSuite) SetupTest() {
	t := s.T()
	testkit.Isolate(t)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s.repo = testkit.NewRepository(t, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": snapshotRules})
	s.loads = 0
	s.original = loadPolicyRules
	loadPolicyRules = func(ctx context.Context, root string, opts types.VCSOptions) (*WorkspaceRules, error) {
		s.loads++
		return s.original(ctx, root, opts)
	}
}

func (s *PolicySnapshotSuite) TearDownTest() {
	loadPolicyRules = s.original
}

func (s *PolicySnapshotSuite) wiring(repo *testkit.Repo) Wiring {
	return Wiring{Root: repo.Root()}
}

// warm writes the snapshot as `agent harness install` does and forgets the
// load it cost.
func (s *PolicySnapshotSuite) warm(repo *testkit.Repo) {
	s.Require().NoError(WritePolicySnapshot(s.T().Context(), s.wiring(repo)))
	s.loads = 0
}

func (s *PolicySnapshotSuite) command(deps Dependencies) types.GuardVerdict {
	s.Require().NotNil(deps.CommandRule)
	got, err := deps.CommandRule(s.T().Context(), types.CommandRequest{Command: "ls"}, hint.NewGate(s.T().TempDir(), "s"))
	s.Require().NoError(err)
	return got
}

func (s *PolicySnapshotSuite) TestAHitJudgesWithoutALoad() {
	fresh, err := s.original(s.T().Context(), s.repo.Root(), types.VCSOptions{})
	s.Require().NoError(err)
	s.warm(s.repo)

	deps, rules := NewDependencies(s.T().Context(), s.wiring(s.repo))
	s.Nil(rules, "a hit loads no rules up front")
	s.Zero(s.loads)
	s.Require().Len(deps.ShellRules, 1)
	s.Equal("no-frobnicate", deps.ShellRules[0].Name)
	s.NotEmpty(deps.Builtins, "the light level sets rules")
	s.Equal(fresh.Policy().Digest, deps.Policy().Digest)
	s.Equal([]string{filepath.Join(s.repo.Root(), "magusfile.buzz")}, deps.Policy().Files)
	approved, err := deps.ApprovedCommandRule(s.T().Context())
	s.Require().NoError(err)
	s.Nil(approved, "a clean tree's approved rule is the working tree's")
	s.Zero(s.loads)

	s.Equal(types.GuardVerdict{Decision: types.GuardDeny, Reason: "committed"}, s.command(deps))
	s.command(deps)
	s.Equal(1, s.loads, "the function rule loads once, on its first call")
}

func (s *PolicySnapshotSuite) TestAnEditedPolicySourceMisses() {
	s.warm(s.repo)
	s.repo.Magusfile(snapshotShellOnly)

	deps, rules := NewDependencies(s.T().Context(), s.wiring(s.repo))
	s.NotNil(rules)
	s.Equal(1, s.loads)
	s.Nil(deps.CommandRule, "the edited tree registers no command rule")
}

func (s *PolicySnapshotSuite) TestANewHeadMisses() {
	s.warm(s.repo)
	s.repo.Commit("next", map[string]string{"README.md": "next\n"})

	_, rules := NewDependencies(s.T().Context(), s.wiring(s.repo))
	s.NotNil(rules)
	s.Equal(1, s.loads)
}

func (s *PolicySnapshotSuite) TestAnotherWorktreeMisses() {
	s.warm(s.repo)
	other := s.repo.AddWorktree("other")

	_, rules := NewDependencies(s.T().Context(), s.wiring(other))
	s.NotNil(rules, "the same commit and bytes in another worktree are its own policy")
	s.Equal(1, s.loads)

	s.loads = 0
	_, rules = NewDependencies(s.T().Context(), s.wiring(s.repo))
	s.Nil(rules, "the first worktree's snapshot still answers it")
	s.Zero(s.loads)
}

func (s *PolicySnapshotSuite) TestATamperedEntryDoesNotVerify() {
	s.warm(s.repo)
	store, err := interp.GuardPolicyStore(s.repo.Root())
	s.Require().NoError(err)
	data, err := os.ReadFile(store.Path)
	s.Require().NoError(err)
	s.Require().Contains(string(data), `"command":true`)
	tampered := strings.ReplaceAll(string(data), `"command":true`, `"command":false`)
	s.Require().NoError(os.WriteFile(store.Path, []byte(tampered), 0o600))

	deps, rules := NewDependencies(s.T().Context(), s.wiring(s.repo))
	s.NotNil(rules, "an entry that does not verify is a miss")
	s.Equal(1, s.loads)
	s.NotNil(deps.CommandRule)
}

// Until rules declare the lines they judge, a line is unclaimed when no
// function rule is registered to judge it. The shell rules are data and
// judge it from the snapshot.
func (s *PolicySnapshotSuite) TestAnUnclaimedLineNeverLoads() {
	s.repo.Commit("shell only", map[string]string{"magusfile.buzz": snapshotShellOnly})
	s.warm(s.repo)

	deps, _ := NewDependencies(s.T().Context(), s.wiring(s.repo))
	s.Nil(deps.CommandRule)
	approved, err := deps.ApprovedCommandRule(s.T().Context())
	s.Require().NoError(err)
	s.Nil(approved)
	verdict := matchWorkspaceShell(deps.ShellRules, "frobnicate --now", deps.ShellDialect)
	s.Equal("Not here.", verdict.Deny)
	s.Zero(s.loads)
}

// The approved side of a tree with a pending edit answers from the pending
// set the snapshot kept. With the index unreadable a status fails, so the
// approved rule can only have resolved without one.
func (s *PolicySnapshotSuite) TestAWarmSnapshotResolvesTheApprovedSideWithoutAVCSStatus() {
	s.repo.Magusfile(snapshotShellOnly)
	s.warm(s.repo)
	s.Require().NoError(os.WriteFile(filepath.Join(s.repo.Root(), ".git", "index"), []byte("not an index"), 0o600))

	cold, err := s.original(s.T().Context(), s.repo.Root(), types.VCSOptions{})
	s.Require().NoError(err)
	_, err = cold.ApprovedCommandRule(s.T().Context())
	s.Require().Error(err, "without the snapshot the approved side needs a status")

	deps, rules := NewDependencies(s.T().Context(), s.wiring(s.repo))
	s.Nil(rules)
	approved, err := deps.ApprovedCommandRule(s.T().Context())
	s.Require().NoError(err)
	s.Require().NotNil(approved, "the committed command rule still judges")
	got, err := approved(s.T().Context(), types.CommandRequest{Command: "ls"}, hint.NewGate(s.T().TempDir(), "s"))
	s.Require().NoError(err)
	s.Equal(types.GuardVerdict{Decision: types.GuardDeny, Reason: "committed"}, got)
	s.Zero(s.loads, "the working tree's load is never needed: it registers no command rule")
}
