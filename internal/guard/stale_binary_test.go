package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
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

// installBinary leaves an executable ./magus in root.
func installBinary(t *testing.T, root string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte("#!/bin/sh\n"), 0o755))
}

// judgeUnloaded judges line in a session of its own, so each deny is a first firing.
func judgeUnloaded(ctx context.Context, deps Dependencies, line string) Verdict {
	return Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: line})
}

// shortDeny asserts the inline shape of a first firing under rule: the verdict opening
// with say, nothing but the nothing-ran note between it and the ref, and no served next.
// It returns the full verdict stored behind the ref.
func shortDeny(t *testing.T, cacheDir string, v Verdict, rule denyRuleName, say string) string {
	t.Helper()
	assert.Equal(t, verdictWithRule("deny", string(rule)), unworded(v))
	ref := verdictRef.FindString(v.Reason)
	require.NotEmpty(t, ref, "the deny cites its stored verdict: %q", v.Reason)
	shown, cited, ok := strings.Cut(v.Reason, "\nfull verdict: ")
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
func servedDeny(t *testing.T, cacheDir string, v Verdict, rule denyRuleName, say string, next hint.Next) string {
	t.Helper()
	require.Len(t, v.Next, 1, v.Reason)
	assert.Equal(t, next, v.Next[0])
	v.Next = nil
	assert.Equal(t, verdictWithRule("deny", string(rule)), unworded(v))
	shown, cited, ok := strings.Cut(v.Reason, "\nfull verdict: ")
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

// The verdicts a stale-binary deny opens with, by cause and caller.
const (
	staleOwnSay    = "./magus is older than this workspace and cannot load its guard policy; rebuild ./magus with `./magus run go-build .`."
	noBinaryOwnSay = "this checkout has no ./magus to load its guard policy; bootstrap ./magus with `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`."
	staleWorkerSay = "./magus is older than this workspace and cannot load its guard policy; ask the main session to place a ./magus that loads this workspace."
	// workerPlacementRun is the one command a leased worker is served in place of a build.
	workerPlacementRun = `"<main checkout>/magus" buzz hack/dev/bootstrap-worktree.buzz -- --job lease-a --from "<main checkout>"`
)

// A fresh worktree has no ./magus and no recorded policy: the magusfile showing a guard
// rule is the only evidence there is, and the judge is a PATH magus that cannot load it.
// Writes are denied; reads and the bootstrap pass.
func TestFreshWorktreeWithoutABinaryDeniesWrites(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, false)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(errMissingMember)

	for _, line := range []string{"touch notes.txt", "git commit -m x", "./magus agent install", "git push origin HEAD:topic"} {
		stored := shortDeny(t, cacheDir, judgeUnloaded(ctx, deps, line), denyRuleStaleBinary, noBinaryOwnSay)
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
		assert.NotEqual(t, "deny", v.Decision, "%s: %s", line, v.Reason)
	}

	edit := Judge(ctx, deps, Request{Input: filepath.Join(root, "main.go"), IsPath: true, Host: "claude-code", Session: "s1"})
	stored := shortDeny(t, cacheDir, edit, denyRuleStaleBinary, noBinaryOwnSay)
	assert.True(t, strings.HasPrefix(stored, noBinaryOwnSay+"\nthis file write changes state"), stored)
}

// A failure that is not stale-shaped still denies in a checkout with no ./magus, and says
// so rather than blaming the version.
func TestUnloadedTreeWithoutABinaryNamesTheMissingBinary(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, false)
	declareGuardRule(t, root)

	v := judgeUnloaded(ctx, unloadedDepsFor(errors.New("magusfile.buzz:3:1: expected expression")), "touch notes.txt")
	stored := shortDeny(t, cacheDir, v, denyRuleStaleBinary, noBinaryOwnSay)
	assert.NotContains(t, stored, "older than")
}

// With no guard rule showing and none ever recorded there is no policy to have lost, so a
// binary that cannot load the tree denies nothing.
func TestUnloadedTreeWithoutAVisibleRuleDeniesNothing(t *testing.T) {
	ctx, _ := spawnFixture(t)
	ownCheckout(t, ctx, false)
	v := judgeUnloaded(ctx, unloadedDepsFor(errMissingMember), "touch notes.txt")
	assert.NotEqual(t, "deny", v.Decision, v.Reason)
}

// A ./magus older than the tree denies an edit, a spawn, a push and a state-writing magus
// verb, and passes the reads and the rebuild.
func TestStaleBinaryDeniesEveryWriteSeam(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	for _, line := range []string{"git push origin HEAD:topic", "./magus agent harness install", "./magus config set --global key=log.format,value=json", "touch notes.txt"} {
		stored := shortDeny(t, cacheDir, judgeUnloaded(ctx, deps, line), denyRuleStaleBinary, staleOwnSay)
		assert.Contains(t, stored, "move the binary aside and bootstrap, one command at a time: `mv magus magus.old`", line)
	}

	spawn := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.True(t, strings.HasPrefix(shortDeny(t, cacheDir, spawn, denyRuleStaleBinary, staleOwnSay), staleOwnSay+"\na subagent spawn changes state"))

	edit := Judge(ctx, deps, Request{Input: filepath.Join(root, "main.go"), IsPath: true, Host: "claude-code", Session: "s1"})
	assert.Contains(t, shortDeny(t, cacheDir, edit, denyRuleStaleBinary, staleOwnSay), "\nthis file write changes state")

	for _, line := range []string{
		"ls", "git status", "git diff --stat", "./magus describe job x", "./magus affected --explain test", "./magus run go-build .", "mv magus magus.old",
		"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .",
	} {
		v := judgeUnloaded(ctx, deps, line)
		assert.NotEqual(t, "deny", v.Decision, "%s: %s", line, v.Reason)
	}
}

// A repeat within a session shortens to the rule and what it catches, like every other
// catalogued deny.
func TestStaleBinaryRepeatNamesTheRule(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	first := Judge(ctx, deps, Request{Input: "touch a", Host: "claude-code", Session: "s1"})
	shortDeny(t, cacheDir, first, denyRuleStaleBinary, staleOwnSay)
	repeat := Judge(ctx, deps, Request{Input: "touch b", Host: "claude-code", Session: "s1"})
	doc, ok := Rule(string(denyRuleStaleBinary))
	require.True(t, ok)
	assert.True(t, strings.HasPrefix(repeat.Reason, "denied again [stale-binary]: "+doc.Catches+"\n"), repeat.Reason)
}

// The remedy follows who is asking: the main session or a person rebuilds, a leased worker
// is served the placement of its base's binary, which passes, and neither the worker's
// deny nor its bootstrap names a build.
func TestStaleBinaryRemedyFollowsTheCaller(t *testing.T) {
	ctx, _ := fleetFixture(t, fleetLeases()[0])
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	shortDeny(t, cacheDir, judgeUnloaded(ctx, deps, "touch notes.txt"), denyRuleStaleBinary, staleOwnSay)

	worker := Judge(ctx, deps, Request{Input: "touch notes.txt", Host: "claude-code", Session: "s1", Lease: "lease-a"})
	stored := servedDeny(t, cacheDir, worker, denyRuleStaleBinary, staleWorkerSay, placementNext(denyRuleStaleBinary, "lease-a"))
	assert.Contains(t, stored, "\n"+onePerBase+"\n")
	assert.NotContains(t, stored, "go-build")

	placed := Judge(ctx, deps, Request{Input: workerPlacementRun, Host: "claude-code", Session: "s1", Lease: "lease-a"})
	assert.NotEqual(t, "deny", placed.Decision, placed.Reason)

	for i, line := range []string{
		"./magus run go-build .",
		"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .",
		"go build -o magus ./cmd/magus",
	} {
		v := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "w" + strconv.Itoa(i), Lease: "lease-a"})
		assert.Equal(t, "deny", v.Decision, line)
		assert.NotContains(t, v.Reason, "rebuild ./magus", line)
	}
}

// A read-only scout may still record its work while the binary is stale: `job exec`,
// `job exit` and `buzz --record` pass for it, and stay denied for a lease that writes.
func TestStaleBinaryLetsAReadOnlyScoutRecord(t *testing.T) {
	scout := types.Job{ID: "scout", ReadOnly: true, State: types.StateRunning, Registered: 1}
	ctx, _ := fleetFixture(t, fleetLeases()[0], scout)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	for _, line := range []string{
		"./magus job exec scout",
		"./magus job exit scout --stdin <<'EOF'\n{}\nEOF",
		"./magus buzz --record probe.buzz",
		"export BAGGAGE=magus.lease=scout && ./magus job exit scout --stdin <<'EOF'\n{}\nEOF",
	} {
		v := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: line, Lease: "scout"})
		assert.NotEqual(t, "deny", v.Decision, "%s: %s", line, v.Reason)

		writer := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "writer " + line, Lease: "lease-a"})
		assert.Equal(t, "deny", writer.Decision, line)
	}
	for _, line := range []string{"./magus buzz probe.buzz", "./magus job fork x --read-only", "./magus job exit scout > out.json"} {
		v := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: line, Lease: "scout"})
		assert.Equal(t, "deny", v.Decision, line)
	}
}

// The raw bootstrap in a binary-less checkout is advised through for the main session and
// refused for a leased worker, who is handed the command the main session places it with.
func TestBootstrapIsTheMainSessionsNotAWorkers(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()[0])
	// The bootstrap rule reads the directory the call runs in, which the fleet fixture leaves unset.
	ctx = WithLocation(ctx, hookLocation(ctx, Dependencies{}).cacheDir, root, root)
	ownCheckout(t, ctx, false)
	line := "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache ."

	deps := strict(testDependencies())

	orchestrator := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "s1"})
	assert.Equal(t, "advise", orchestrator.Decision, orchestrator.Reason)

	worker := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "s1", Lease: "lease-a"})
	stored := servedDeny(t, hookLocation(ctx, Dependencies{}).cacheDir, worker, denyRuleRawTool,
		"a worker does not build magus; ask the main session to place ./magus", placementNext(denyRuleRawTool, "lease-a"))
	assert.Contains(t, stored, "\n"+onePerBase+"\n")
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
	}, clearVolatile(spawns[0]))
}

// clearVolatile zeroes what the clock, the OS account and the content-addressed blobs decide,
// so a recorded event compares whole and a field added to it cannot be dropped unseen.
func clearVolatile(e trail.Event) trail.Event {
	e.Ts, e.Origin, e.Workspace, e.DurationMs = 0, types.Origin{}, "", 0
	e.RequestRef, e.ResponseRef, e.RequestBytes, e.ResponseBytes = "", "", 0, 0
	e.Preview, e.VerdictRef = "", ""
	return e
}

func TestUnloadCause(t *testing.T) {
	bare, withBinary := t.TempDir(), t.TempDir()
	installBinary(t, withBinary)
	stale := types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "older")
	typo := errors.New("magusfile.buzz:3:1: expected expression")

	assert.Equal(t, causeStale, unloadCause(stale, withBinary))
	assert.Equal(t, causeStale, unloadCause(errMissingMember, withBinary), "the uncoded missing member reads as stale")
	assert.Equal(t, causeNoBinary, unloadCause(typo, bare), "any failure in a checkout with no ./magus")
	assert.Equal(t, "", unloadCause(typo, withBinary), "a typo is the author's to fix")
	assert.Equal(t, "", unloadCause(nil, bare))
	assert.Equal(t, "", unloadCause(stale, ""))
}

func TestDeclaresGuardRule(t *testing.T) {
	root := t.TempDir()
	assert.False(t, declaresGuardRule(root), "no magusfile")
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n"), 0o644))
	assert.False(t, declaresGuardRule(root), "a magusfile that registers none")
	declareGuardRule(t, root)
	assert.True(t, declaresGuardRule(root))

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "magusfiles"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfiles", "guard.buzz"), []byte("magus\\guard.write(w);\n"), 0o644))
	assert.True(t, declaresGuardRule(dir), "the magusfiles/ form")
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
	deps := unloadedDepsFor(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

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
		stored := shortDeny(t, cacheDir, v, denyRuleStaleBinary, staleOwnSay)
		assert.Contains(t, stored, "\nthis call to the magus "+strings.Fields(name)[0]+" tool changes state", name)
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
		assert.NotEqual(t, "deny", v.Decision, "%s: %s", name, v.Reason)
	}
}

// A leased worker is told to ask the main session for a binary, never to build one.
func TestStaleBinaryMCPDenyFollowsTheCaller(t *testing.T) {
	ctx, _ := fleetFixture(t, fleetLeases()[0])
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	worker := Judge(ctx, deps, Request{Input: mcpEnvelope(t, "client", map[string]any{"script": "x"}), Host: "claude-code", Session: "s1", Lease: "lease-a"})
	stored := servedDeny(t, hookLocation(ctx, Dependencies{}).cacheDir, worker, denyRuleStaleBinary, staleWorkerSay, placementNext(denyRuleStaleBinary, "lease-a"))
	assert.NotContains(t, stored, "go-build")

	reads := Judge(ctx, deps, Request{Input: mcpEnvelope(t, "status", map[string]any{}), Host: "claude-code", Session: "s1", Lease: "lease-a"})
	assert.NotEqual(t, "deny", reads.Decision, reads.Reason)
}

// Every magus MCP tool is classified, so a new one cannot reach a stale binary unjudged.
func TestEveryMCPToolIsClassifiedForAStaleBinary(t *testing.T) {
	for _, tool := range hint.AllToolNames {
		_, ok := mcpReadOnlyTools[tool]
		assert.True(t, ok, "%s has no entry in mcpReadOnlyTools", tool)
	}
	assert.Len(t, mcpReadOnlyTools, len(hint.AllToolNames), "an entry names no tool")
}

func TestRecoveryLine(t *testing.T) {
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
		{"mv magus magus.old", "", true},
		{"mv magus other", "", false},
		{"mv magus magus.old && rm -rf .", "", false},
		{"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .", "", true},
		{"go run -trimpath ./cmd/magus run go-build --no-cache .", "", false},
		{"GOEXPERIMENT=jsonv2 go build -trimpath -o magus ./cmd/magus", "", true},
		{"go build -o magus ./cmd/magus", "", true},
		{"go test ./...", "", false},
		{"./magus run go-build .", "lease-a", false},
		{"mv magus magus.old", "lease-a", false},
		{"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .", "lease-a", false},
		{workerPlacementRun, "lease-a", true},
		{"/repo/magus buzz hack/dev/bootstrap-worktree.buzz -- --job lease-a --from /repo", "lease-a", true},
		{"/repo/magus buzz hack/dev/bootstrap-worktree.buzz -- --job lease-a --from /repo && go build ./cmd/magus", "lease-a", false},
		{"/repo/magus buzz hack/dev/other.buzz", "lease-a", false},
		{"/repo/magus buzz hack/dev/bootstrap-worktree.buzz -- --job x --from /repo", "", false},
	} {
		assert.Equal(t, tc.want, recoveryLine(tc.line, DialectBash, tc.lease), "%s (lease %q)", tc.line, tc.lease)
	}
}
