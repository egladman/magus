package guard

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	typesagent "github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// The recorded rule set is the guard's own state, private to its owner.
func TestPolicyMarkerIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	path := filepath.Join(t.TempDir(), policyMarkerFile)
	writePolicyMarker(path, policyMarker{})

	fi, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm())
}

// The marker is a JSON Lines file whose last line is the marker; a marker that did not change
// is not appended again.
func TestPolicyMarkerIsAppendedAsJSONLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), policyMarkerFile)
	first := policyMarker{Digest: "d1", ShellRules: 3, SpawnRule: true, Pending: true, Files: []string{"/w/magusfile.buzz"}}
	second := policyMarker{Digest: "d2", ShellRules: 3, SpawnRule: true, Files: []string{"/w/magusfile.buzz", "/w/rules/a b.buzz"}}
	writePolicyMarker(path, first)
	writePolicyMarker(path, first)
	writePolicyMarker(path, second)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, `{"digest":"d1","shell_rules":3,"spawn_rule":true,"pending":true,"files":["/w/magusfile.buzz"]}`+"\n"+
		`{"digest":"d2","shell_rules":3,"spawn_rule":true,"pending":false,"files":["/w/magusfile.buzz","/w/rules/a b.buzz"]}`+"\n", string(body))
	got, ok := readPolicyMarker(path)
	require.True(t, ok)
	assert.Equal(t, second, got)
}

func TestPolicyMarkerThatDoesNotParseIsNotARecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), policyMarkerFile)
	require.NoError(t, os.WriteFile(path, []byte("digest\td1\n"), 0o600))

	_, ok := readPolicyMarker(path)
	assert.False(t, ok)
}

func TestPolicyMarkerPreservesLegacyState(t *testing.T) {
	path := filepath.Join(t.TempDir(), policyMarkerFile)
	old := filepath.Join(filepath.Dir(path), "guard-policy.json")
	const held = "{\"digest\":\"older\"}\n"
	require.NoError(t, os.WriteFile(old, []byte(held), 0o600))
	_, err := policyMarkerLog(path).Load()
	require.ErrorIs(t, err, file.ErrLegacyStore)
	_, ok := readPolicyMarker(path)
	assert.False(t, ok)
	writePolicyMarker(path, policyMarker{Digest: "new"})
	assert.NoFileExists(t, path)
	raw, err := os.ReadFile(old)
	require.NoError(t, err)
	assert.Equal(t, held, string(raw))
}

// sourcesAt builds the lazy approved-id callback a hook hands RecordPolicy, counting how
// often it runs: it costs a process per file, so steady state must not call it.
func sourcesAt(calls *int, pairs ...PolicySource) func(context.Context) []PolicySource {
	return func(context.Context) []PolicySource {
		*calls++
		return pairs
	}
}

func policyActions(t *testing.T, base string) []string {
	t.Helper()
	var out []string
	for _, e := range trailEvents(t, base, trail.KindGuardPolicy) {
		out = append(out, e.Action)
	}
	return out
}

// One event per change and none on a steady state, which is what makes the trail read
// as the policy's lineage rather than as a log of hook calls.
func TestRecordPolicyWritesOneEventPerChange(t *testing.T) {
	base := t.TempDir()
	calls := 0
	clean := sourcesAt(&calls, PolicySource{Path: "/w/magusfile.buzz", Worktree: "a", Approved: "a"})

	assert.Empty(t, RecordPolicy(t.Context(), base, "/w", PolicyState{}, false), "no rules, nothing to record")
	assert.Empty(t, policyActions(t, base))

	state := PolicyState{Digest: "d1", SpawnRule: true, Sources: clean}
	assert.Equal(t, "d1", RecordPolicy(t.Context(), base, "/w", state, false))
	for range 5 {
		RecordPolicy(t.Context(), base, "/w", state, false)
		RecordPolicy(t.Context(), base, "/w", state, true)
	}
	assert.Equal(t, []string{PolicyLoaded}, policyActions(t, base))
	assert.Equal(t, 1, calls, "a steady state with nothing pending reads no approved ids")
}

// The lineage an uncommitted edit leaves: tightened when it adds, loosen_pending when it
// removes, committed once the approved sources catch up, removed when nothing is left.
func TestRecordPolicyClassifiesTheChange(t *testing.T) {
	base := t.TempDir()
	calls := 0
	clean := sourcesAt(&calls, PolicySource{Path: "/w/magusfile.buzz", Worktree: "a", Approved: "a"})
	dirty := sourcesAt(&calls, PolicySource{Path: "/w/magusfile.buzz", Worktree: "b", Approved: "a"})
	ctx := t.Context()

	RecordPolicy(ctx, base, "/w", PolicyState{Digest: "d1", ShellRules: 1, Sources: clean}, false)
	RecordPolicy(ctx, base, "/w", PolicyState{Digest: "d2", ShellRules: 1, SpawnRule: true, Sources: dirty}, false)
	RecordPolicy(ctx, base, "/w", PolicyState{Digest: "d3", ShellRules: 1, Sources: dirty}, false)

	before := calls
	RecordPolicy(ctx, base, "/w", PolicyState{Digest: "d3", ShellRules: 1, Sources: clean}, false)
	assert.Equal(t, before, calls, "a command call does not re-read a pending edit")
	RecordPolicy(ctx, base, "/w", PolicyState{Digest: "d3", ShellRules: 1, Sources: clean}, true)

	RecordPolicy(ctx, base, "/w", PolicyState{Sources: clean}, false)

	assert.Equal(t, []string{PolicyLoaded, PolicyTightened, PolicyLoosenPending, PolicyCommitted, PolicyRemoved}, policyActions(t, base))
}

// Shell rules have no approved twin, so dropping one applies at once even while another
// edit is pending.
func TestRecordPolicyShellRuleDropIsCommitted(t *testing.T) {
	base := t.TempDir()
	calls := 0
	dirty := sourcesAt(&calls, PolicySource{Path: "/w/magusfile.buzz", Worktree: "b", Approved: "a"})
	RecordPolicy(t.Context(), base, "/w", PolicyState{Digest: "d1", ShellRules: 2, SpawnRule: true, Sources: dirty}, false)
	RecordPolicy(t.Context(), base, "/w", PolicyState{Digest: "d2", ShellRules: 1, SpawnRule: true, Sources: dirty}, false)
	assert.Equal(t, []string{PolicyLoaded, PolicyCommitted}, policyActions(t, base))
}

// A guard_policy event names sources by blob id and carries both digests, never a body.
func TestRecordPolicyEventCarriesRefsNotBodies(t *testing.T) {
	base := t.TempDir()
	calls := 0
	src := PolicySource{Path: "/w/magusfile.buzz", Worktree: "b", Approved: "a"}
	RecordPolicy(t.Context(), base, "/w", PolicyState{Digest: "d1", SpawnRule: true, Sources: sourcesAt(&calls)}, false)
	RecordPolicy(t.Context(), base, "/w", PolicyState{Digest: "d2", SpawnRule: true, Sources: sourcesAt(&calls, src)}, false)

	events := trailEvents(t, base, trail.KindGuardPolicy)
	require.Len(t, events, 2)
	var tightened trail.Event
	for _, e := range events {
		if e.Action == PolicyTightened {
			tightened = e
		}
	}
	require.NotEmpty(t, tightened.RequestRef)
	assert.Equal(t, "d2", tightened.PolicyDigest)
	body, err := trail.ReadBlob(base, tightened.RequestRef)
	require.NoError(t, err)
	var got policyEvent
	require.NoError(t, json.Unmarshal(body, &got))
	assert.Equal(t, policyEvent{APIVersion: policyAPIVersion, Action: PolicyTightened, Digest: "d2", Previous: "d1", Sources: []PolicySource{src}}, got)
}

// Every verdict event names the policy in force and which side decided it.
func TestAToolTimeRecordsTheToolsRunTimeAndJudgesNothing(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)

	v := Judge(ctx, Dependencies{}, Request{Input: "Bash", ToolMs: 420, Host: "claude-code", Session: "s1", Event: "PostToolUse"})

	assert.Equal(t, decisionPass, v.Decision)
	commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.Len(t, commands, 1)
	got := commands[0]
	got.Ts, got.Workspace, got.Binary, got.BinaryVersion, got.Cwd = 0, "", "", "", ""
	got.User, got.UID, got.RequestRef, got.ResponseRef = "", "", "", ""
	got.RequestBytes, got.ResponseBytes = 0, 0
	assert.Equal(t, trail.Event{
		Kind:       trail.KindAgentCommand,
		Origin:     types.Origin{EntryPoint: types.EntryPointHook, Host: "claude-code", Session: "s1"},
		Action:     "Bash",
		Outcome:    trail.OutcomeOK,
		DurationMs: 420,
		Preview:    "observed",
	}, got)
}

func TestAToolTimeKeepsTheDigestsItWasGiven(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)

	Judge(ctx, Dependencies{}, Request{Input: "Bash", ToolMs: 420, CallDigest: "c1", ResultDigest: "r1", Host: "claude-code", Session: "s1", Event: "PostToolUse"})

	calls := trail.ForSession(cacheDir, "s1", 10).Calls
	require.Len(t, calls, 1)
	got := calls[0]
	got.At = time.Time{}
	assert.Equal(t, trail.SessionCall{Action: "Bash", ToolMs: 420, AgentFacts: types.AgentFacts{
		CallDigest: "c1", ResultDigest: "r1", Thread: types.AgentThreadMain, ThreadFrom: types.ThreadInferred,
	}}, got)
}

func TestAToolTimeNamesTheDigestsItWasNotGiven(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)

	Judge(ctx, Dependencies{}, Request{Input: "Bash", ToolMs: 420, Host: "claude-code", Session: "s1", Event: "PostToolUse"})
	Judge(ctx, Dependencies{}, Request{Input: "Bash", ToolMs: 420, CallDigest: "c1", Host: "claude-code", Session: "s1", Event: "PostToolUse"})

	calls := trail.ForSession(cacheDir, "s1", 10).Calls
	require.Len(t, calls, 2)
	assert.Equal(t, []string{"call", "result"}, calls[0].Unobserved)
	assert.Equal(t, []string{"result"}, calls[1].Unobserved)
}

func TestVerdictEventsCarryThePolicy(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	deps := Dependencies{Policy: func() PolicyState { return PolicyState{Digest: "d9", ShellRules: 1} }}

	Judge(ctx, deps, Request{Input: "git stash", Host: "claude-code", Session: "s1"})
	commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.Len(t, commands, 1)
	// The clock, the stamped account, the workspace and the content-addressed fields are
	// cleared first, so a field added to the event cannot be dropped here unseen.
	e := commands[0]
	e.Ts, e.Origin, e.Workspace, e.GuardMs = 0, types.Origin{}, "", 0
	e.RequestRef, e.ResponseRef, e.RequestBytes, e.ResponseBytes = "", "", 0, 0
	e.Preview, e.VerdictRef = "", ""
	assert.NotEmpty(t, e.Cwd, "a bare command records the directory the judge ran in")
	assert.NotEmpty(t, e.Phases)
	e.Cwd, e.Phases = "", nil
	assert.Equal(t, trail.Event{
		Kind:         trail.KindAgentCommand,
		Action:       "shell.command",
		Outcome:      trail.OutcomeOK,
		PolicyDigest: "d9",
		DecidedBy:    decidedByBuiltin,
	}, e)

	deps.ShellRules = []WorkspaceShellRule{{Name: "no-curl", Decision: "deny", Program: "curl", Reason: "not here"}}
	Judge(ctx, deps, Request{Input: "curl https://example.com", Host: "claude-code", Session: "s1"})
	commands = trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.Len(t, commands, 2)
	var byWorkspace int
	for _, e := range commands {
		if e.DecidedBy == decidedByWorktree {
			byWorkspace++
		}
	}
	assert.Equal(t, 1, byWorkspace, "a workspace shell rule's deny is the working tree's")
}

// The marker outlives a load failure, so it answers which seams the policy registered the
// last time it loaded; no marker registered nothing.
func TestPolicyRecordedRule(t *testing.T) {
	base := t.TempDir()
	for _, seam := range []functionSeam{seamSpawn, seamCommand, seamWrite} {
		assert.False(t, recordedRule(base, seam), "no marker")
	}
	assert.False(t, recordedRule("", seamCommand), "no cache dir")

	RecordPolicy(t.Context(), base, "/w", PolicyState{Digest: "d1", CommandRule: true, WriteRule: true}, false)
	assert.Equal(t, []bool{false, true, true},
		[]bool{recordedRule(base, seamSpawn), recordedRule(base, seamCommand), recordedRule(base, seamWrite)})

	// A load failure hands RecordPolicy no state, so the record stands.
	recordPolicy(t.Context(), Dependencies{}, location{cacheDir: base}, true)
	assert.True(t, recordedRule(base, seamCommand))
}

// A policy source is recognized through a symlinked spelling of the workspace, and a
// relative write path is the calling directory's, not the workspace root's.
func TestIsGuardSourceResolvesSymlinksAndTheCallDirectory(t *testing.T) {
	root := t.TempDir()
	real, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	link := filepath.Join(t.TempDir(), "link")
	require.NoError(t, os.Symlink(root, link))
	at := location{workspace: link, dir: filepath.Join(link, "hack")}
	deps := Dependencies{Policy: func() PolicyState { return PolicyState{Files: []string{"hack/policy/guard.buzz"}} }}

	assert.True(t, isGuardSource(deps, at, filepath.Join(real, "magusfile.buzz")), "the root magusfile")
	assert.True(t, isGuardSource(deps, at, filepath.Join(real, "hack", "policy", "guard.buzz")), "a source the load read")
	assert.True(t, isGuardSource(deps, at, filepath.Join("policy", "guard.buzz")), "relative to the call's directory")
	assert.False(t, isGuardSource(deps, at, filepath.Join("hack", "policy", "guard.buzz")), "not relative to the workspace root")
}

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

func (s *PolicySnapshotSuite) TestAHitKeepsTheStepRulesKinds() {
	s.repo.Commit("step", map[string]string{"magusfile.buzz": `import "magus";

magus\guard.step("refund", fun (req: StepRequest) > GuardVerdict => magus\guard.deny("No {req.step.name} refunds."));
`})
	s.warm(s.repo)

	deps, rules := NewDependencies(s.T().Context(), s.wiring(s.repo))
	s.Nil(rules, "a hit loads no rules up front")
	s.Equal([]string{"refund"}, deps.Policy().StepRules)
	s.Require().NotNil(deps.StepRule)
	s.Nil(deps.StepRule("egress"), "a kind the snapshot does not name has no rule")
	rule := deps.StepRule("refund")
	s.Require().NotNil(rule)
	s.Zero(s.loads)

	got, err := rule(s.T().Context(), typesagent.StepRequest{Step: typesagent.Step{Kind: "refund", Name: "card"}}, hint.NewGate(s.T().TempDir(), "s"))
	s.Require().NoError(err)
	s.Equal(types.GuardVerdict{Decision: types.GuardDeny, Reason: "No card refunds."}, got)
	s.Equal(1, s.loads, "the step rule loads on its first call")
	approved, err := deps.ApprovedStepRule(s.T().Context(), "refund")
	s.Require().NoError(err)
	s.Nil(approved, "a clean tree's approved rule is the working tree's")
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
