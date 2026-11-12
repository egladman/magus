package guard

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// errMissingMember is what a ./magus built before `magus\guard.builtins` reports for a tree
// that calls it: no BZZ code, only the sentence.
var errMissingMember = errors.New(`magusfile: exec magusfile.buzz: buzz: line 15:8: object guard has no field or method "builtins"`)

// declareGuardRule gives root a magusfile that registers a guard rule, which is all a
// fresh worktree shows before any policy has loaded in its cache.
func declareGuardRule(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\nmagus\\guard.command(judge);\n"), 0o644))
}

// judgeUnloaded judges line in a session of its own, so each deny is a first firing.
func judgeUnloaded(ctx context.Context, deps Dependencies, line string) types.GuardJudgment {
	return Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: line})
}

// shortDeny asserts the inline shape of a first firing under rule: the verdict opening
// with say, nothing but the nothing-ran note between it and the ref, and no served next.
// It returns the full verdict stored behind the ref.
func shortDeny(t *testing.T, cacheDir string, v types.GuardJudgment, rule denyRuleName, say string) string {
	t.Helper()
	assert.Equal(t, verdictWithRule(decisionDeny, string(rule)), unworded(v))
	ref := verdictRef.FindString(v.Reason)
	require.NotEmpty(t, ref, "the deny cites its stored verdict: %q", v.Reason)
	shown, cited, ok := strings.Cut(v.Reason, "\ndetails: ")
	require.True(t, ok, v.Reason)
	assert.Equal(t, hint.NextForDenial(ref).Run, cited)
	verdict, note, _ := strings.Cut(shown, "\n")
	assert.Equal(t, say, verdict)
	assert.NotContains(t, note, "\n", "only the nothing-ran line sits between the verdict and the ref")
	stored, err := trail.ReadBlob(cacheDir, ref)
	require.NoError(t, err)
	return string(stored)
}

// servedDeny is shortDeny for a deny that serves next: the one command sits between the
// verdict and the ref, and is the verdict's only served next.
func servedDeny(t *testing.T, cacheDir string, v types.GuardJudgment, rule denyRuleName, say string, next types.NextStep) string {
	t.Helper()
	require.Len(t, v.Next, 1, v.Reason)
	assert.Equal(t, next, v.Next[0])
	v.Next = nil
	assert.Equal(t, verdictWithRule(decisionDeny, string(rule)), unworded(v))
	shown, cited, ok := strings.Cut(v.Reason, "\ndetails: ")
	require.True(t, ok, v.Reason)
	ref := verdictRef.FindString(cited)
	require.NotEmpty(t, ref, "the deny cites its stored verdict: %q", v.Reason)
	assert.Equal(t, hint.NextForDenial(ref).Run, cited)
	before, ok := strings.CutSuffix(shown, "\nnext:\n  "+next.Run)
	require.True(t, ok, "the one command closes the inline verdict: %q", shown)
	verdict, note, _ := strings.Cut(before, "\n")
	assert.Equal(t, say, verdict)
	assert.NotContains(t, note, "\n", "only the nothing-ran line sits between the verdict and the command")
	stored, err := trail.ReadBlob(cacheDir, ref)
	require.NoError(t, err)
	return string(stored)
}

// workerPlacementRun is the one command a leased worker is served in place of a build.
const workerPlacementRun = `"<main checkout>/magus" buzz hack/dev/bootstrap-worktree.buzz -- --job lease-a --from "<main checkout>"`

// staleOwnSay(root) is the verdict a stale-binary deny opens with in the
// checkout of magus at root, whoever asks.
func staleOwnSay(root string) string {
	return fmt.Sprintf("./magus was built from another contract than this tree carries and cannot load its guard policy; "+
		"relink ./magus, alone on its line: `%s`.", ward.RelinkAt(root))
}

// noBinaryOwnSay is the verdict a checkout of magus at root with no ./magus opens with.
func noBinaryOwnSay(root string) string {
	return fmt.Sprintf("this checkout has no ./magus to load its guard policy; bootstrap ./magus with `GOEXPERIMENT=jsonv2 go -C %s run -trimpath ./cmd/magus run go-build --no-cache .`, then ready the checkout with `%s`, each alone on its line.", shellQuoted(root), readyCheckoutLine(root))
}

// shellQuoted is path as one POSIX shell word, the way a remedy line names a checkout.
func shellQuoted(path string) string {
	q, _ := syntax.Quote(path, syntax.LangPOSIX) // only a NUL byte fails, and no temp dir holds one
	return q
}

// A fresh worktree has no ./magus and no recorded policy: the magusfile showing a guard
// rule is the only evidence there is, and the judge is a PATH magus that cannot load it.
// Writes are denied; reads and the bootstrap pass.
func TestFreshWorktreeWithoutABinaryDeniesWrites(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, false)
	declareGuardRule(t, root)
	deps := unloadedDependencies(errMissingMember)

	for _, line := range []string{"touch notes.txt", "git commit -m x", "./magus agent install", "git push origin HEAD:topic"} {
		stored := shortDeny(t, cacheDir, judgeUnloaded(ctx, deps, line), denyRuleStaleBinary, noBinaryOwnSay(root))
		assert.Contains(t, stored, "changes state, and the guard policy that would judge it is not running:", line)
		assert.Contains(t, stored, "\n  worktree: the magusfile failed to load: "+errMissingMember.Error(), line)
		assert.Contains(t, stored, "see: "+ruleDocsBase+"stale-binary/", line)
	}

	for _, line := range []string{
		"ls -la",
		"git status",
		"git log --oneline -3",
		"cat go.mod | head -3",
		"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .",
	} {
		v := judgeUnloaded(ctx, deps, line)
		assert.NotEqual(t, decisionDeny, v.Decision, "%s: %s", line, v.Reason)
	}

	edit := Judge(ctx, deps, Request{Input: filepath.Join(root, "main.go"), IsPath: true, Host: "claude-code", Session: "s1"})
	stored := shortDeny(t, cacheDir, edit, denyRuleStaleBinary, noBinaryOwnSay(root))
	assert.True(t, strings.HasPrefix(stored, noBinaryOwnSay(root)+"\nthis file write changes state"), stored)
}

// A failure that is not stale-shaped still denies in a checkout with no ./magus, and says
// so rather than blaming the version.
func TestUnloadedTreeWithoutABinaryNamesTheMissingBinary(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, false)
	declareGuardRule(t, root)

	v := judgeUnloaded(ctx, unloadedDependencies(errors.New("magusfile.buzz:3:1: expected expression")), "touch notes.txt")
	stored := shortDeny(t, cacheDir, v, denyRuleStaleBinary, noBinaryOwnSay(root))
	assert.NotContains(t, stored, "older than")
}

// PATH magus in a checkout that builds magus from source answers only the
// bootstrap, even when its own policy would load.
func TestPathMagusInAFromSourceCheckoutJudgesNothing(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)

	own := filepath.Join(root, "magus")
	same := ForeignJudgeDeps(Dependencies{}, root, own)
	require.NoError(t, same.LoadFailure, "the checkout's own ./magus judges")

	deps := ForeignJudgeDeps(unloadedDependencies(errStaleLoad), root, "/usr/local/bin/magus")
	require.ErrorIs(t, deps.LoadFailure, errNotCheckoutBinary)
	assert.Equal(t, unloadNoBinary, unloadCause(deps.LoadFailure, root))

	v := judgeUnloaded(ctx, deps, "touch notes.txt")
	assert.Equal(t, decisionDeny, v.Decision, v.Reason)
	assert.Contains(t, v.Reason, "relink ./magus, alone on its line")
}

// No rule showing or recorded still denies a change while nothing loads.
func TestUnloadedTreeWithoutAVisibleRuleDeniesAChange(t *testing.T) {
	ctx, _ := spawnFixture(t)
	ownCheckout(t, ctx, false)
	v := judgeUnloaded(ctx, unloadedDependencies(errMissingMember), "touch notes.txt")
	assert.Equal(t, decisionDeny, v.Decision, v.Reason)
}

// A ./magus older than the tree denies an edit, a spawn, a push and a state-writing magus
// verb, and passes the reads and the rebuild.
func TestStaleBinaryDeniesEveryWriteSeam(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDependencies(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	for _, line := range []string{"git push origin HEAD:topic", "./magus agent harness install", "./magus config set --global key=log.format,value=json", "touch notes.txt"} {
		stored := shortDeny(t, cacheDir, judgeUnloaded(ctx, deps, line), denyRuleStaleBinary, staleOwnSay(root))
		assert.Contains(t, stored, "\n"+relinkWhy+"\n", line)
		assert.NotContains(t, stored, "magus.old", line)
	}

	spawn := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.True(t, strings.HasPrefix(shortDeny(t, cacheDir, spawn, denyRuleStaleBinary, staleOwnSay(root)), staleOwnSay(root)+"\na subagent spawn changes state"))

	edit := Judge(ctx, deps, Request{Input: filepath.Join(root, "main.go"), IsPath: true, Host: "claude-code", Session: "s1"})
	assert.Contains(t, shortDeny(t, cacheDir, edit, denyRuleStaleBinary, staleOwnSay(root)), "\nthis file write changes state")

	for _, line := range []string{
		"ls", "git status", "git diff --stat", "./magus describe job x", "./magus affected --explain test", "./magus run go-build .",
		"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .",
	} {
		v := judgeUnloaded(ctx, deps, line)
		assert.NotEqual(t, decisionDeny, v.Decision, "%s: %s", line, v.Reason)
	}
}

// A repeat within a session shortens to the rule and what it catches, like every other
// catalogued deny.
func TestStaleBinaryRepeatNamesTheRule(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDependencies(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	first := Judge(ctx, deps, Request{Input: "touch a", Host: "claude-code", Session: "s1"})
	shortDeny(t, cacheDir, first, denyRuleStaleBinary, staleOwnSay(root))
	repeat := Judge(ctx, deps, Request{Input: "touch b", Host: "claude-code", Session: "s1"})
	doc, ok := Rule(string(denyRuleStaleBinary))
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(repeat.Reason, fmt.Sprintf("denied again [stale-binary]: %s\n", doc.Catches)), repeat.Reason)
}

// StaleBinaryRemedySuite pins the fix a stale ./magus names: the relink for the
// root, and the placement for a worker, whose relink no row would pin.
type StaleBinaryRemedySuite struct {
	suite.Suite
	layers   ctxLayers
	cacheDir string
	root     string
	deps     Dependencies
}

func TestStaleBinaryRemedySuite(t *testing.T) {
	suite.Run(t, new(StaleBinaryRemedySuite))
}

func (s *StaleBinaryRemedySuite) SetupTest() {
	s.layers, _ = fleetLayers(s.T(), fixture.FleetLeases()[0])
	s.cacheDir = hookLocation(s.layers.of(s.T()), Dependencies{}).cacheDir
	s.root = ownCheckout(s.T(), s.layers.of(s.T()), true)
	declareGuardRule(s.T(), s.root)
	s.deps = unloadedDependencies(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))
}

func (s *StaleBinaryRemedySuite) worker(session, line string) types.GuardJudgment {
	return Judge(s.layers.of(s.T()), s.deps, Request{Input: line, Host: "claude-code", Session: session, Lease: "lease-a"})
}

func (s *StaleBinaryRemedySuite) TestTheRootIsToldToRelink() {
	shortDeny(s.T(), s.cacheDir, judgeUnloaded(s.layers.of(s.T()), s.deps, "touch notes.txt"), denyRuleStaleBinary, staleOwnSay(s.root))
}

func (s *StaleBinaryRemedySuite) TestAWorkerIsToldToAskForAPlacement() {
	v := s.worker("s1", "touch internal/ledger/notes.txt")
	s.Equal(decisionDeny, v.Decision)
	s.Contains(v.Reason, "ask the main session to place a ./magus that loads this workspace")
	s.NotContains(v.Reason, ward.RelinkAt(s.root))
}

func (s *StaleBinaryRemedySuite) TestTheWorkersPlacementPasses() {
	v := s.worker("p", workerPlacementRun)
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *StaleBinaryRemedySuite) TestAWorkersRelinkIsDenied() {
	s.Equal(decisionDeny, s.worker("r", ward.RelinkAt(s.root)).Decision)
}

func (s *StaleBinaryRemedySuite) TestAWorkersRebuildIsDenied() {
	s.Equal(decisionDeny, s.worker("w", "./magus run go-build .").Decision)
}

func (s *StaleBinaryRemedySuite) TestAWorkersBootstrapIsDenied() {
	s.Equal(decisionDeny, s.worker("wb", ward.BootstrapCommand).Decision)
}

// A worker whose placed ./magus was built ahead of its checkout fast-forwards its own branch
// to that commit, which neither the stale binary nor lease-vcs refuses; any other way of
// moving the branch stays denied.
func TestStaleBinaryLetsAWorkerFastForwardToItsBinary(t *testing.T) {
	ctx, _ := fleetFixture(t, fixture.FleetLeases()[0])
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDependencies(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))
	judge := func(session, line string) types.GuardJudgment {
		return Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: session, Lease: "lease-a"})
	}

	for i, line := range []string{"git merge --ff-only f1980cc5c", "git merge f1980cc5c3224cc9fca99c41a9f03415fd77db4b --ff-only"} {
		v := judge("ff"+strconv.Itoa(i), line)
		assert.NotEqual(t, decisionDeny, v.Decision, "%s: %s", line, v.Reason)
	}
	for i, line := range []string{
		"git merge f1980cc5c",
		"git merge --ff-only main",
		"git reset --hard f1980cc5c",
		"git -C ../sibling merge --ff-only f1980cc5c",
		"git merge --ff-only f1980cc5c && git push",
	} {
		v := judge("other"+strconv.Itoa(i), line)
		assert.Equal(t, decisionDeny, v.Decision, line)
	}
}

// A read-only scout may still record its work while the binary is stale: `job exec`,
// `job exit` and `buzz --record` pass for it, and stay denied for a lease that writes.
func TestStaleBinaryLetsAReadOnlyScoutRecord(t *testing.T) {
	scout := types.Job{ID: "scout", ReadOnly: true, State: types.StateRunning, Registered: 1}
	ctx, _ := fleetFixture(t, fixture.FleetLeases()[0], scout)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDependencies(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	for _, line := range []string{
		"./magus job exec scout",
		"./magus job exit scout --stdin <<'EOF'\n{}\nEOF",
		"./magus buzz --record probe.buzz",
		"export BAGGAGE=magus.lease=scout && ./magus job exit scout --stdin <<'EOF'\n{}\nEOF",
	} {
		v := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: line, Lease: "scout"})
		assert.NotEqual(t, decisionDeny, v.Decision, "%s: %s", line, v.Reason)

		writer := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "writer " + line, Lease: "lease-a"})
		assert.Equal(t, decisionDeny, writer.Decision, line)
	}
	for _, line := range []string{"./magus buzz probe.buzz", "./magus job fork x --read-only", "./magus job exit scout > out.json"} {
		v := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: line, Lease: "scout"})
		assert.Equal(t, decisionDeny, v.Decision, line)
	}
}

// The raw bootstrap in a binary-less checkout is advised through for the main session and
// refused for a leased worker, who is handed the command the main session places it with.
func TestBootstrapIsTheMainSessionsNotAWorkers(t *testing.T) {
	ctx, root := fleetFixture(t, fixture.FleetLeases()[0])
	// The bootstrap rule reads the directory the call runs in, which the fleet fixture leaves unset.
	ctx = WithLocation(ctx, hookLocation(ctx, Dependencies{}).cacheDir, root, root)
	ownCheckout(t, ctx, false)
	line := "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache ."

	deps := strict(testDependencies())

	orchestrator := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "s1"})
	assert.Equal(t, decisionAdvise, orchestrator.Decision, orchestrator.Reason)

	worker := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "s1", Lease: "lease-a"})
	stored := servedDeny(t, hookLocation(ctx, Dependencies{}).cacheDir, worker, denyRuleRawTool,
		"a worker does not build magus; ask the main session to place ./magus", placementNext(denyRuleRawTool, "lease-a"))
	assert.Contains(t, stored, "\n"+onePerBase+"\n")
}

// Over a ./magus of another contract the bootstrap is refused for anyone,
// naming the relink, which then passes for the main session alone.
func TestRelinkReplacesTheBootstrapOverAStaleBinary(t *testing.T) {
	ctx, root := fleetFixture(t, fixture.FleetLeases()[0])
	ctx = WithLocation(ctx, hookLocation(ctx, Dependencies{}).cacheDir, root, root)
	ownCheckout(t, ctx, true)
	loads := 0
	deps := loadingAs(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "out of date"), &loads)
	bootstrap := "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache ."

	orchestrator := Judge(ctx, deps, Request{Input: bootstrap, Host: "claude-code", Session: "s1"})
	assert.Equal(t, decisionDeny, orchestrator.Decision, orchestrator.Reason)
	assert.Contains(t, orchestrator.Reason, ward.RelinkAt(root))

	worker := Judge(ctx, deps, Request{Input: bootstrap, Host: "claude-code", Session: "s1", Lease: "lease-a"})
	assert.Equal(t, decisionDeny, worker.Decision, worker.Reason)

	unleased := Judge(ctx, deps, Request{Input: ward.RelinkAt(""), Host: "claude-code", Session: "r"})
	assert.NotEqual(t, decisionDeny, unleased.Decision, unleased.Reason)
	leased := Judge(ctx, deps, Request{Input: ward.RelinkAt(""), Host: "claude-code", Session: "rlease-a", Lease: "lease-a"})
	assert.Equal(t, decisionDeny, leased.Decision, "a worker's relink leaves a binary no row pins")
}

// Each hook row records the magus that judged the call and the directory the host said it
// ran in, so a verdict can be tied to the build that reached it.
func TestHookRowsRecordTheJudgingBinaryAndCwd(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	deps := Dependencies{Binary: "/work/tree/magus", BinaryVersion: "v0.5.0-rc.3-62-gcb43888d2"}
	cwd := filepath.Join(hookLocation(ctx, deps).workspace, "console")

	Judge(ctx, deps, Request{Host: "claude-code", Input: hookJSON(t, map[string]any{
		"session_id": "s1", "hook_event_name": "PreToolUse", "tool_name": "Bash", "cwd": cwd,
		"tool_input": map[string]any{"command": "ls"},
	})})
	Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})

	commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.Len(t, commands, 1)
	assert.Equal(t, trail.Event{
		Kind:          trail.KindAgentCommand,
		Action:        "shell.command",
		Outcome:       trail.OutcomeOK,
		Binary:        "/work/tree/magus",
		BinaryVersion: "v0.5.0-rc.3-62-gcb43888d2",
		Cwd:           cwd,
		Phases:        []trail.Phase{{Name: phaseBuiltinRules}, {Name: phaseNotices}, {Name: phaseJudge}},
	}, clearVolatile(commands[0]))

	spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
	require.Len(t, spawns, 1)
	assert.Equal(t, trail.Event{
		Kind:          trail.KindAgentSpawn,
		Action:        "general-purpose",
		Outcome:       trail.OutcomeOK,
		Binary:        "/work/tree/magus",
		BinaryVersion: "v0.5.0-rc.3-62-gcb43888d2",
		Cwd:           "/Users/dev/repo", // the spawn envelope's reported directory
		Phases:        []trail.Phase{{Name: phaseJudge}},
	}, clearVolatile(spawns[0]))
}

// A glue that forwards only the command or the path sends no cwd, and runs magus in the
// call's directory instead, so the row records the directory the judge ran in.
func TestBareHookRowsRecordTheDirectoryTheJudgeRanIn(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	deps := Dependencies{Binary: "/work/tree/magus"}
	wd, err := os.Getwd()
	require.NoError(t, err)

	Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "notes.txt", IsPath: true})

	commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.Len(t, commands, 2)
	byTool := map[string]string{}
	for _, e := range commands {
		byTool[e.Action] = e.Cwd
	}
	assert.Equal(t, map[string]string{
		"shell.command": cmp.Or(hookLocation(ctx, deps).dir, wd),
		"file.write":    wd,
	}, byTool)
}

// The rule load the caller timed leads the row's phases, and every row ends with the whole
// judgment, so a slow verdict shows where its time went.
func TestHookRowsRecordEachPhase(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	probe := &commandRuleProbe{}
	deps := Dependencies{
		CommandRule: probe.rule(),
		RuleLoad:    trail.Phase{Name: phaseLoadRules, ElapsedMs: 812, BudgetMs: 6500},
	}

	Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "ls"})

	commands := trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.Len(t, commands, 1)
	phases := commands[0].Phases
	require.Len(t, phases, 5)
	assert.Equal(t, trail.Phase{Name: phaseLoadRules, ElapsedMs: 812, BudgetMs: 6500}, phases[0])
	assert.Equal(t, phaseWorktreeRule, phases[1].Name)
	assert.Positive(t, phases[1].BudgetMs)
	assert.False(t, phases[1].Lapsed)
	assert.Equal(t, phaseBuiltinRules, phases[2].Name)
	assert.Equal(t, phaseNotices, phases[3].Name)
	assert.Equal(t, phaseJudge, phases[4].Name)
}

// clearVolatile zeroes what the clock, the OS account and the content-addressed blobs decide,
// so a recorded event compares whole and a field added to it cannot be dropped unseen. A
// phase keeps its name and whether it lapsed.
func clearVolatile(e trail.Event) trail.Event {
	e.Ts, e.Origin, e.Workspace, e.GuardMs = 0, types.Origin{}, "", 0
	e.RequestRef, e.ResponseRef, e.RequestBytes, e.ResponseBytes = "", "", 0, 0
	e.Preview, e.VerdictRef = "", ""
	phases := make([]trail.Phase, len(e.Phases))
	for i, p := range e.Phases {
		phases[i] = trail.Phase{Name: p.Name, Lapsed: p.Lapsed}
	}
	e.Phases = phases
	return e
}

// UnloadCauseSuite pins that the contracts, never the failure's wording,
// say a load failure is the judging binary's.
type UnloadCauseSuite struct {
	suite.Suite
	stale error
}

func TestUnloadCauseSuite(t *testing.T) {
	suite.Run(t, new(UnloadCauseSuite))
}

func (s *UnloadCauseSuite) SetupTest() {
	s.stale = types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "older")
}

// withBinary is a workspace holding a ./magus.
func (s *UnloadCauseSuite) withBinary() string {
	w := testkit.NewWorkspace(s.T())
	w.WriteExecutable("magus", "#!/bin/sh\n")
	return w.Root()
}

func (s *UnloadCauseSuite) TestAnotherContractIsStaleWhateverTheErrorSays() {
	root := s.withBinary()
	carryContract(s.T(), root, "config another.key\n")
	s.Equal(unloadStale, unloadCause(errors.New("magusfile.buzz:3:1: expected expression"), root))
}

func (s *UnloadCauseSuite) TestTheSameContractIsTheTreesOwnFailure() {
	root := s.withBinary()
	carryOwnContract(s.T(), root)
	s.Equal(unloadNone, unloadCause(s.stale, root))
	s.Equal(unloadNone, unloadCause(errMissingMember, root), "the wording is never read")
}

func (s *UnloadCauseSuite) TestATreeCarryingNoContractIsTheTreesOwnFailure() {
	s.Equal(unloadNone, unloadCause(s.stale, s.withBinary()))
}

func (s *UnloadCauseSuite) TestAnyFailureWithNoBinaryHasNone() {
	s.Equal(unloadNoBinary, unloadCause(errors.New("typo"), testkit.NewWorkspace(s.T()).Root()))
}

func (s *UnloadCauseSuite) TestNoFailureOrNoRootIsNone() {
	s.Equal(unloadNone, unloadCause(nil, testkit.NewWorkspace(s.T()).Root()))
	s.Equal(unloadNone, unloadCause(s.stale, ""))
}

func (s *UnloadCauseSuite) TestAForeignJudgeOfTheSameContractJudges() {
	root := s.withBinary()
	require.NoError(s.T(), os.WriteFile(filepath.Join(root, "go.mod"), fmt.Appendf(nil, "module %s\n", ownModule), 0o644))
	carryOwnContract(s.T(), root)
	s.False(foreignJudge(root, "/usr/local/bin/magus"))
	carryContract(s.T(), root, "config another.key\n")
	s.True(foreignJudge(root, "/usr/local/bin/magus"))
}

func TestReadOnlyLine(t *testing.T) {
	for line, want := range map[string]bool{
		"ls -la":                          true,
		"git status":                      true,
		"git -C ../other log --oneline":   true,
		"git diff HEAD~1 | head -20":      true,
		"git branch --list":               true,
		"git stash list":                  true,
		"git diff --output=out.patch":     false,
		"git branch -D old":               false,
		"git commit -m x":                 false,
		"git stash":                       false,
		"git -c alias.st=push st":         false,
		"cat a.txt b.txt > c.txt":         false,
		"cat a.txt > /dev/null":           true,
		"echo hi 2>&1":                    true,
		"echo hi >> log.txt":              false,
		"sed -n 1,20p main.go":            true,
		"sed -i s/a/b/ main.go":           false,
		"find . -name '*.go'":             true,
		"find . -name '*.tmp' -delete":    false,
		"find . -exec rm {} +":            false,
		"find . -exec grep x {} +":        true,
		"grep -rn foo . | wc -l":          true,
		"sort -o out.txt in.txt":          false,
		"rm -rf build":                    false,
		"touch a":                         false,
		"mv a b":                          false,
		"cd sub && ls":                    true,
		"echo $(rm -rf x)":                false,
		"ls & sleep 1":                    false,
		"gh pr view 412":                  true,
		"gh pr merge 412":                 false,
		"gh api -X DELETE /repos/x":       false,
		"./magus describe job x":          true,
		"./magus query output out1":       true,
		"./magus run go-build .":          false,
		"./magus run test . --dry-run":    true,
		"./magus affected --explain test": true,
		"./magus affected test":           false,
		"./magus job exit scout":          false,
		"./magus buzz --record p.buzz":    false,
		"./magus agent install":           false,
		"./magus refs foo --rename bar":   false,
		"magus --root . ls":               true,
		"env FOO=1 ls":                    true,
		"bash -c 'rm -rf x'":              false,
		"bash -c 'ls'":                    true,
		"FOO=1 ls":                        true,
		"`ls`":                            false,
		"while read l; do echo  $l; done": true,
		"while read l; do rm $l; done":    false,
		"if true; then ls; fi":            true,
		"f() { rm x; }":                   false,
		"unterminated 'quote":             false,
		"git $SUB":                        false,
		"git status && git push":          false,
		"hg status":                       true,
		"jj log":                          true,
		"jj git push":                     false,
		"go test ./...":                   false,
		"mise exec -- git status":         true,
		"time git status":                 true,
		"git worktree list":               true,
		"git worktree remove ../wt":       false,
		"git config --get user.name":      true,
		"git config user.name x":          false,
		"git tag":                         true,
		"git tag v1":                      false,
		"git remote -v":                   true,
		"git remote add o url":            false,
		"git checkout -b topic":           false,
		"git show HEAD:file":              true,
		"git rev-parse --show-toplevel":   true,
		"git ls-files -m":                 true,
		"git grep -n foo":                 true,
		"git reflog":                      true,
		"git fetch":                       false,
		"git blame -L 1,5 main.go":        true,
		"printf '%s' x":                   true,
		"printf '%s' x > out":             false,
		"cat <<EOF\nhello\nEOF":           true,
		"cat <<EOF > out\nhello\nEOF":     false,
		"test -f go.mod && echo yes":      true,
		"pwd; ls":                         true,
		"git log -1 --format=%H":          true,
	} {
		assert.Equal(t, want, readOnlyLine(line, DialectBash), line)
	}
}

// awk reads when its program is on the line and neither redirects, pipes nor calls system().
func TestReadOnlyLineReadsMagusListings(t *testing.T) {
	for _, line := range []string{"./magus where docs", "./magus vcs ls-files -o name"} {
		assert.True(t, readOnlyLine(line, DialectBash), line)
	}
	assert.False(t, readOnlyLine("./magus vcs resolve a.go", DialectBash))
}

// WorkerRouteSuite pins the one line a worker's own row can always run.
type WorkerRouteSuite struct {
	suite.Suite
	root string
}

func TestWorkerRouteSuite(t *testing.T) {
	suite.Run(t, new(WorkerRouteSuite))
}

func (s *WorkerRouteSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magus"), []byte("#!/bin/sh\n"), 0o755))
}

func (s *WorkerRouteSuite) TestAWorkerWithABinaryAsksForAPlacement() {
	fix := binaryRemedy(true, s.root, "lease-a", false)
	s.NotContains(fix.say, ward.RelinkAt(s.root))
	s.Equal("lease-a", fix.placeFor)
}

func (s *WorkerRouteSuite) TestTheIntegratorWithABinaryIsToldToRelinkIt() {
	fix := binaryRemedy(true, s.root, "lease-a", true)
	s.Contains(fix.say, ward.RelinkAt(s.root))
	s.Empty(fix.placeFor)
}

func (s *WorkerRouteSuite) TestAWorkerWithNoBinaryAsksForAPlacement() {
	s.Require().NoError(os.Remove(filepath.Join(s.root, "magus")))
	fix := binaryRemedy(true, s.root, "lease-a", false)
	s.Equal("lease-a", fix.placeFor)
}

func (s *WorkerRouteSuite) TestTheNamedLinePassesWithTheLeaseExportInFront() {
	declareCheckoutHook(s.T(), s.root)
	at := magusRepoAt
	at.workspace = s.root
	line := "export BAGGAGE=magus.lease=lease-a; " + job.CheckoutBootstrap(testCheckoutHook, s.root)
	s.True(recoveryLine(line, DialectBash, "lease-a", false, at))
}

// The first line is a read pipeline a lapse once denied as state-changing.
func TestReadOnlyLineReadsAnAwkProgram(t *testing.T) {
	for _, line := range []string{
		`sed -n 340,360p f; sed -n 955,990p g; grep -n x g | awk -F: '{print $1}'`,
		`awk -v n=2 '{print $n}' f`,
		`awk -F, -- '{print $2}' f`,
	} {
		assert.True(t, readOnlyLine(line, DialectBash), line)
	}
	for _, line := range []string{
		`awk '{print > "out"}' f`,
		`awk '{print | "sh"}' f`,
		`awk 'BEGIN{system("rm x")}'`,
		`awk -f prog.awk f`,
		`gawk -i inplace '{print}' f`,
		`awk -F:`,
	} {
		assert.False(t, readOnlyLine(line, DialectBash), line)
	}
}

func mcpEnvelope(t *testing.T, tool string, input map[string]any) string {
	t.Helper()
	return hookJSON(t, map[string]any{
		"session_id": "s1", "hook_event_name": "PreToolUse", "tool_name": "mcp__magus__" + tool, "tool_input": input,
	})
}

// A magus MCP tool that changes state is denied while the binary cannot load the tree,
// like the CLI verb it is the other door to; the tools that only read still answer.
func TestStaleBinaryDeniesStateChangingMCPTools(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDependencies(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	for name, call := range map[string]string{
		"client script":     mcpEnvelope(t, "client", map[string]any{"script": "import \"magus\";\nfun main(args: [str]) > void !> any { magus\\cmd(\"agent\", args: [\"install\"]); }"}),
		"client path":       mcpEnvelope(t, "client", map[string]any{"path": "missing.buzz"}),
		"diff comment":      mcpEnvelope(t, "diff", map[string]any{"op": "comment", "path": "a.go", "body": "x"}),
		"diff suggest":      mcpEnvelope(t, "diff", map[string]any{"op": "suggest", "path": "a.go", "reason": "x"}),
		"diff resolve":      mcpEnvelope(t, "diff", map[string]any{"op": "resolve", "id": "c1"}),
		"diff outline":      mcpEnvelope(t, "diff", map[string]any{"op": "outline", "topics": []string{"a"}}),
		"diff unknown op":   mcpEnvelope(t, "diff", map[string]any{"op": "frobnicate"}),
		"client no content": mcpEnvelope(t, "client", map[string]any{}),
	} {
		v := Judge(ctx, deps, Request{Input: call, Host: "claude-code", Session: name})
		stored := shortDeny(t, cacheDir, v, denyRuleStaleBinary, staleOwnSay(root))
		assert.Contains(t, stored, fmt.Sprintf("\nthis call to the magus %s tool changes state", strings.Fields(name)[0]), name)
	}

	for name, call := range map[string]string{
		"status":        mcpEnvelope(t, "status", map[string]any{}),
		"config":        mcpEnvelope(t, "config", map[string]any{}),
		"console":       mcpEnvelope(t, "console", map[string]any{"app": "diff"}),
		"buzz":          mcpEnvelope(t, "buzz", map[string]any{"script": "fun transform(input: any, args: [str]) > any { return input; }"}),
		"diff default":  mcpEnvelope(t, "diff", map[string]any{}),
		"diff state":    mcpEnvelope(t, "diff", map[string]any{"op": " state ", "projection": "summary"}),
		"diff non-text": mcpEnvelope(t, "diff", map[string]any{"op": 7}),
	} {
		v := Judge(ctx, deps, Request{Input: call, Host: "claude-code", Session: "s1"})
		assert.NotEqual(t, decisionDeny, v.Decision, "%s: %s", name, v.Reason)
	}
}

// A leased worker is told to ask for a placement, never to rebuild.
func TestStaleBinaryMCPDenyFollowsTheCaller(t *testing.T) {
	ctx, _ := fleetFixture(t, fixture.FleetLeases()[0])
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDependencies(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	worker := Judge(ctx, deps, Request{Input: mcpEnvelope(t, "client", map[string]any{"script": "x"}), Host: "claude-code", Session: "s1", Lease: "lease-a"})
	want := verdictWithRule(decisionDeny, string(denyRuleStaleBinary))
	want.Next = []types.NextStep{placementNext(denyRuleStaleBinary, "lease-a")}
	assert.Equal(t, want, unworded(worker))
	assert.Contains(t, worker.Reason, "ask the main session to place a ./magus")
	assert.NotContains(t, worker.Reason, ward.RelinkAt(root))

	reads := Judge(ctx, deps, Request{Input: mcpEnvelope(t, "status", map[string]any{}), Host: "claude-code", Session: "s1", Lease: "lease-a"})
	assert.NotEqual(t, decisionDeny, reads.Decision, reads.Reason)
}

// Every magus MCP tool is classified, so a new one cannot reach a stale binary unjudged.
func TestEveryMCPToolIsClassifiedForAStaleBinary(t *testing.T) {
	for _, tool := range hint.AllToolNames {
		_, ok := mcpReadOnlyTools[tool]
		assert.True(t, ok, "%s has no entry in mcpReadOnlyTools", tool)
	}
	assert.Len(t, mcpReadOnlyTools, len(hint.AllToolNames), "an entry names no tool")
}

// Memory and a scratchpad sit under no workspace, so no unloaded policy holds them back.
func TestOutsideAnyWorkspace(t *testing.T) {
	ws := testkit.NewWorkspace(t)
	ws.Write("magus.yaml", "{}\n")
	at := location{workspace: ws.Root(), dir: ws.Root()}
	scratch := t.TempDir()
	assert.True(t, outsideAnyWorkspace(filepath.Join(scratch, "notes", "memory.md"), at))
	assert.False(t, outsideAnyWorkspace(filepath.Join(ws.Root(), "internal", "x.go"), at))
	assert.False(t, outsideAnyWorkspace("relative.go", at), "relative to the workspace")
	assert.False(t, outsideAnyWorkspace("$HOME/x", at), "a place the line does not determine")
}

func TestRecoveryLine(t *testing.T) {
	at := magusRepoAt
	at.workspace = t.TempDir()
	declareCheckoutHook(t, at.workspace)
	for _, tc := range []struct {
		line, lease string
		builds      bool
		want        bool
	}{
		{"git cherry-pick --abort", "lease-a", false, true},
		{"GIT_EDITOR=true git cherry-pick --continue", "lease-a", false, true},
		{"git merge --abort", "", true, true},
		{"git rebase --continue", "lease-a", false, true},
		{"git revert --abort", "lease-a", false, true},
		{"git worktree add ../scratch HEAD", "lease-a", false, true},
		{"git cherry-pick abc123", "lease-a", false, true},
		{"git cherry-pick -x abc123 def456", "lease-a", false, true},
		{"git cherry-pick --skip", "lease-a", false, true},
		{"git cherry-pick --strategy=ours abc123", "lease-a", false, false},
		{"git cherry-pick --abort && git push", "lease-a", false, false},
		{"FOO=1 git cherry-pick --continue", "lease-a", false, false},
		{"git worktree remove ../scratch", "lease-a", false, false},
		{"git merge --ff-only f1980cc5c", "lease-a", false, true},
		{"git merge --ff-only f1980cc5c", "lease-a", true, true},
		{"git merge --ff-only f1980cc5c", "", true, false},
		{"FOO=1 git merge --ff-only f1980cc5c", "lease-a", false, false},
		{"git merge f1980cc5c", "lease-a", false, false},
		{"git merge --no-ff f1980cc5c", "lease-a", false, false},
		{"git merge --ff-only origin/main", "lease-a", false, false},
		{"git merge --ff-only f1980cc5c && git push", "lease-a", false, false},
		{"./magus run go-build . -s", "lease-a", true, true},
		{"GOEXPERIMENT=jsonv2 go build -trimpath -o magus ./cmd/magus", "lease-a", true, true},
		{workerPlacementRun, "lease-a", true, true},
		{"./magus run test .", "lease-a", true, false},
	} {
		assert.Equal(t, tc.want, recoveryLine(tc.line, DialectBash, tc.lease, tc.builds, at), "%s (lease %q, builds %v)", tc.line, tc.lease, tc.builds)
	}
	for _, tc := range []struct {
		line, lease string
		want        bool
	}{
		{"./magus run go-build .", "", true},
		{"magus run go-build", "", true},
		{"./magus run go::go-build .", "", true},
		{"./magus run go-build . -s", "", true},
		{"./magus run test .", "", false},
		{"./magus run go-build docs", "", false},
		{"./magus run go-build . && ls", "", false},
		{"mv magus magus.old", "", false},
		{"mv magus other", "", false},
		{"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .", "", true},
		{"go run -trimpath ./cmd/magus run go-build --no-cache .", "", false},
		{"GOEXPERIMENT=jsonv2 go build -trimpath -o magus ./cmd/magus", "", true},
		{"go build -o magus ./cmd/magus", "", true},
		{"go test ./...", "", false},
		{"./magus run go-build .", "lease-a", false},
		{"mv magus magus.old", "lease-a", false},
		{"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .", "lease-a", false},
		{"GOEXPERIMENT=jsonv2 go build -trimpath -o magus ./cmd/magus", "lease-a", false},
		{"GOEXPERIMENT=jsonv2 go -C /elsewhere build -trimpath -o magus ./cmd/magus", "lease-a", false},
		{workerPlacementRun, "lease-a", true},
		{"/repo/magus buzz hack/dev/bootstrap-worktree.buzz -- --job lease-a --from /repo", "lease-a", true},
		{"/repo/magus buzz hack/dev/bootstrap-worktree.buzz -- --job lease-a --from /repo && go build ./cmd/magus", "lease-a", false},
		{"/repo/magus buzz hack/dev/other.buzz", "lease-a", false},
		{"./magus buzz hack/dev/bootstrap-checkout.buzz -- --here", "lease-a", true},
		{"./magus buzz hack/dev/bootstrap-checkout.buzz -- --checkout /w/a", "lease-a", true},
		{"./magus buzz hack/dev/bootstrap-checkout.buzz -- --here && git push", "lease-a", false},
		{"/repo/magus buzz hack/dev/bootstrap-worktree.buzz -- --job x --from /repo", "", true},
	} {
		assert.Equal(t, tc.want, recoveryLine(tc.line, DialectBash, tc.lease, tc.lease == "", at), "%s (lease %q)", tc.line, tc.lease)
	}
}

// A stale-binary deny never names a command the same rule would deny.
func TestRecoveryLinePassesEveryCommandTheRemedyNames(t *testing.T) {
	for _, tc := range []struct {
		name, lease string
		builds      bool
	}{{"root", "", true}, {"integrator", "lease-a", true}, {"worker", "lease-a", false}} {
		for _, hasBinary := range []bool{true, false} {
			root := t.TempDir()
			if hasBinary {
				require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), nil, 0o755))
			}
			at := magusRepoAt
			at.workspace, at.dir = root, root
			fix := binaryRemedy(true, root, tc.lease, tc.builds)
			var named []string
			switch {
			case hasBinary && tc.builds:
				named = []string{ward.RelinkAt(root), ward.RelinkAt("")}
			case tc.builds:
				named = []string{ward.BootstrapCommand, config.SourceCommand(root, "", "", true)}
			default:
				named = []string{strings.ReplaceAll(strings.Join(placementArgv(tc.lease), " "), "<main checkout>", "/repo")}
			}
			for _, line := range named {
				assert.True(t, recoveryLine(line, DialectBash, tc.lease, tc.builds, at), "%s (binary %v) names %q: %s", tc.name, hasBinary, line, fix.say)
			}
		}
	}
}

// A rebuild judged while the knowledge store could not vouch for the target policies
// carries the note saying how they were read into the stale-binary deny.
func TestStaleBinaryDenialNamesThePolicyFallback(t *testing.T) {
	at := location{policies: func() declaredIndex { return declaredIndex{note: policyUnreadNote} }}
	call := commandCall(context.Background(), commandRuleInput{command: "./magus run go-build .", dialect: DialectBash}, at)
	assert.Equal(t, policyUnreadNote, call.policyNote)

	d := staleBinaryDenial(call, unloadStale, nil, false, t.TempDir())
	assert.True(t, strings.HasSuffix(d.Why, "\n"+policyUnreadNote), d.Why)
}

// LineWorkspaceSuite places shell lines run from a session checkout against a job checkout.
type LineWorkspaceSuite struct {
	suite.Suite
	session, job string
}

func TestLineWorkspaceSuite(t *testing.T) {
	suite.Run(t, new(LineWorkspaceSuite))
}

func (s *LineWorkspaceSuite) SetupTest() {
	s.session = testkit.NewWorkspace(s.T()).Root()
	s.job = testkit.NewWorkspace(s.T()).Root()
	for _, root := range []string{s.session, s.job} {
		s.Require().NoError(os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("{}\n"), 0o644))
	}
}

func (s *LineWorkspaceSuite) TestACdMovesTheLineIntoTheJobCheckout() {
	s.Equal(s.job, LineWorkspace(fmt.Sprintf("cd %s && ./magus run lint . && git status", s.job), s.session))
}

func (s *LineWorkspaceSuite) TestARootFlagMovesAMagusCall() {
	s.Equal(s.job, LineWorkspace(fmt.Sprintf("magus --root %s run lint .", s.job), s.session))
}

func (s *LineWorkspaceSuite) TestALineThatStaysIsTheSessionCheckout() {
	s.Equal(s.session, LineWorkspace("./magus run lint .", s.session))
}

func (s *LineWorkspaceSuite) TestCallsInTwoCheckoutsPlaceNone() {
	s.Empty(LineWorkspace(fmt.Sprintf("git status && cd %s && ./magus run lint .", s.job), s.session))
}

func (s *LineWorkspaceSuite) TestACdInsideAConditionalPlacesNone() {
	s.Empty(LineWorkspace(fmt.Sprintf("if true; then cd %s; fi; ./magus run lint .", s.job), s.session))
}

func (s *LineWorkspaceSuite) TestALineWithNoCallPlacesNone() {
	s.Empty(LineWorkspace("cd "+s.job, s.session))
}

func (s *LineWorkspaceSuite) TestARelativeSessionPlacesNone() {
	s.Empty(LineWorkspace("./magus run lint .", "relative"))
}

// LoadRecoverySuite judges the ways back while ./magus cannot load
// a checkout that is a git repository with two commits.
type LoadRecoverySuite struct {
	suite.Suite
	layers     ctxLayers
	deps       Dependencies
	checkpoint string
	root       string
}

func TestLoadRecoverySuite(t *testing.T) {
	suite.Run(t, new(LoadRecoverySuite))
}

func (s *LoadRecoverySuite) SetupTest() {
	ctx, cacheDir := spawnFixture(s.T())
	repo := testkit.NewRepository(s.T(), nil)
	s.checkpoint = repo.Head()
	repo.Commit("breaks the load", map[string]string{"notes.txt": "x\n"})
	s.root = repo.Root()
	ctx = WithLocation(ctx, cacheDir, repo.Root(), repo.Root())
	ownCheckout(s.T(), ctx, true)
	declareGuardRule(s.T(), repo.Root())
	s.layers = append(locationLayers(ctx), s.pin(s.checkpoint))
	s.deps = unloadedDependencies(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))
}

// pin answers the job store with one row recording checkpoint.
func (s *LoadRecoverySuite) pin(checkpoint string) func(context.Context) context.Context {
	row := types.Job{ID: "base", State: types.StateRunning, Registered: 1, Checkpoint: checkpoint}
	return snapshotLayer(job.Snapshot{Rows: []types.Job{row}})
}

func (s *LoadRecoverySuite) judge(line string) types.GuardJudgment {
	return Judge(s.testContext(), s.deps, Request{Input: line, Host: "claude-code", Session: line})
}

func (s *LoadRecoverySuite) TestAnAbortPasses() {
	for _, verb := range []string{"merge", "cherry-pick", "rebase"} {
		v := s.judge(fmt.Sprintf("git %s --abort", verb))
		s.NotEqual(decisionDeny, v.Decision, "%s: %s", verb, v.Reason)
	}
}

func (s *LoadRecoverySuite) TestAKeepResetToACheckpointPasses() {
	v := s.judge("git reset --keep " + s.checkpoint[:9])
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *LoadRecoverySuite) TestAMergeResetToACheckpointPasses() {
	v := s.judge("git reset --merge " + s.checkpoint)
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *LoadRecoverySuite) TestAResetToARevisionNoRowRecordsIsDenied() {
	s.Equal(decisionDeny, s.judge("git reset --keep HEAD").Decision)
}

func (s *LoadRecoverySuite) TestAResetToADirtyCheckpointIsDenied() {
	s.push()
	s.layers = append(s.layers, s.pin(s.checkpoint+"+0123abcd"))
	s.Equal(decisionDeny, s.judge("git reset --keep "+s.checkpoint).Decision)
}

func (s *LoadRecoverySuite) git(args ...string) string {
	out, err := exec.Command("git", append([]string{"-C", s.root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...).CombinedOutput()
	s.Require().NoError(err, "%s", out)
	return strings.TrimSpace(string(out))
}

// push publishes HEAD, so no commit on the branch is the caller's alone.
func (s *LoadRecoverySuite) push() {
	remote := s.T().TempDir()
	s.git("init", "-q", "--bare", remote)
	s.git("remote", "add", "origin", remote)
	s.git("push", "-q", "origin", "HEAD:refs/heads/main")
}

func (s *LoadRecoverySuite) TestARevertOfAnUnpushedCommitPasses() {
	head := s.git("rev-parse", "HEAD")
	for _, line := range []string{"git revert " + head, "git revert --no-edit " + head[:9]} {
		v := s.judge(line)
		s.NotEqual(decisionDeny, v.Decision, "%s: %s", line, v.Reason)
	}
}

func (s *LoadRecoverySuite) TestARevertOfAPushedCommitIsDenied() {
	s.push()
	s.Equal(decisionDeny, s.judge("git revert "+s.git("rev-parse", "HEAD")).Decision)
}

func (s *LoadRecoverySuite) TestARevertOfSeveralCommitsIsDenied() {
	s.Equal(decisionDeny, s.judge("git revert HEAD HEAD~1").Decision)
}

func (s *LoadRecoverySuite) TestAKeepResetOverUnpushedCommitsPasses() {
	broken := s.git("rev-parse", "HEAD")
	s.git("commit", "-q", "--allow-empty", "-m", "more")
	v := s.judge("git reset --keep " + broken)
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *LoadRecoverySuite) TestAKeepResetOverPushedCommitsIsDenied() {
	broken := s.git("rev-parse", "HEAD")
	s.git("commit", "-q", "--allow-empty", "-m", "more")
	s.push()
	s.Equal(decisionDeny, s.judge("git reset --keep "+broken).Decision)
}

func (s *LoadRecoverySuite) TestAKeepResetToARevisionOffTheBranchIsDenied() {
	s.git("checkout", "-q", "-b", "side", s.checkpoint)
	s.git("commit", "-q", "--allow-empty", "-m", "side")
	side := s.git("rev-parse", "HEAD")
	s.git("checkout", "-q", "-")
	s.Equal(decisionDeny, s.judge("git reset --keep "+side).Decision)
}

func (s *LoadRecoverySuite) TestASoftResetToACheckpointIsDenied() {
	s.Equal(decisionDeny, s.judge("git reset --soft "+s.checkpoint).Decision)
}

func (s *LoadRecoverySuite) TestAChainedResetIsDenied() {
	s.Equal(decisionDeny, s.judge(fmt.Sprintf("git reset --keep %s && touch x", s.checkpoint)).Decision)
}

func (s *LoadRecoverySuite) TestAnAppendOutsideEveryCheckoutPasses() {
	memory := filepath.Join(s.T().TempDir(), "memory.md")
	v := s.judge(fmt.Sprintf("cat >> %s <<'EOF'\nnote\nEOF", shellQuoted(memory)))
	s.NotEqual(decisionDeny, v.Decision, v.Reason)
}

func (s *LoadRecoverySuite) TestARedirectIntoTheCheckoutIsDenied() {
	s.Equal(decisionDeny, s.judge("echo x > notes.txt").Decision)
}

func (s *LoadRecoverySuite) TestAWriteOutsideByAProgramThatWritesIsDenied() {
	memory := filepath.Join(s.T().TempDir(), "memory.md")
	s.Equal(decisionDeny, s.judge("touch "+shellQuoted(memory)).Decision)
}

func (s *LoadRecoverySuite) testContext() context.Context { return s.layers.of(s.T()) }

// NoBinaryRemedySuite judges the fix a magus checkout with no ./magus is named.
type NoBinaryRemedySuite struct {
	suite.Suite
	root string
}

func TestNoBinaryRemedySuite(t *testing.T) {
	suite.Run(t, new(NoBinaryRemedySuite))
}

func (s *NoBinaryRemedySuite) SetupTest() {
	s.root = s.T().TempDir()
	mod := fmt.Appendf(nil, "module %s\n\ngo 1.25\n", ownModule)
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "go.mod"), mod, 0o644))
	declareCheckoutHook(s.T(), s.root)
}

func (s *NoBinaryRemedySuite) TestItNamesTheBootstrapThenTheCheckoutHook() {
	ready := "./magus buzz " + testCheckoutHook + " -- --here --verify-ready"
	bootstrap := config.SourceCommand(s.root, "", "", true)
	fix := binaryRemedy(true, s.root, "", true)
	s.Equal(fmt.Sprintf("bootstrap ./magus with `%s`, then ready the checkout with `%s`, each alone on its line", bootstrap, ready), fix.say)
	at := location{workspace: s.root, policies: magusRepoAt.policies}
	s.True(recoveryLine(ready, DialectBash, "", true, at), ready)
}

// productLineSuite pins that a line running a product path (bin/magus) reads
// as a magus line, so a stale judge holds back its state changes too.
type productLineSuite struct {
	suite.Suite
}

func TestProductLineSuite(t *testing.T) {
	suite.Run(t, new(productLineSuite))
}

func (s *productLineSuite) TestItsReadsAreReadOnly() {
	s.True(readOnlyLine("bin/magus describe job x", DialectBash))
}

func (s *productLineSuite) TestItsJobVerbsChangeState() {
	s.False(readOnlyLine("bin/magus job exit x --stdin", DialectBash))
}

func (s *productLineSuite) TestAWorkerMayNotRecoverThroughIt() {
	at := location{workspace: s.T().TempDir(), policies: magusRepoAt.policies}
	s.False(recoveryLine("bin/magus run go-build .", DialectBash, "lease-b", false, at))
}
