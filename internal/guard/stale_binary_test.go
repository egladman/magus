package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func judgeUnloaded(ctx context.Context, deps Dependencies, line string) Verdict {
	return Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "s1"})
}

// A fresh worktree has no ./magus and no recorded policy: the magusfile showing a guard
// rule is the only evidence there is, and the judge is a PATH magus that cannot load it.
// Writes are denied; reads and the bootstrap pass.
func TestFreshWorktreeWithoutABinaryDeniesWrites(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := ownCheckout(t, ctx, false)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(errMissingMember)

	for _, line := range []string{"touch notes.txt", "git commit -m x", "./magus agent install", "git push origin HEAD:topic"} {
		v := judgeUnloaded(ctx, deps, line)
		assert.Equal(t, "deny", v.Decision, line)
		assert.Contains(t, v.Reason, "This checkout has no ./magus. Bootstrap one: `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`.", line)
		assert.Contains(t, v.Reason, "the magus judging it cannot load this workspace", line)
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
	assert.Equal(t, "deny", edit.Decision)
	assert.Equal(t, workspaceWriteRule, edit.Rule)
}

// A failure that is not stale-shaped still denies in a checkout with no ./magus, and says
// so rather than blaming the version.
func TestUnloadedTreeWithoutABinaryNamesTheMissingBinary(t *testing.T) {
	ctx, _ := spawnFixture(t)
	root := ownCheckout(t, ctx, false)
	declareGuardRule(t, root)

	v := judgeUnloaded(ctx, unloadedDepsFor(errors.New("magusfile.buzz:3:1: expected expression")), "touch notes.txt")
	require.Equal(t, "deny", v.Decision)
	assert.Contains(t, v.Reason, "This checkout has no ./magus, and the magus that answered cannot load its magusfile.")
	assert.NotContains(t, v.Reason, "older than the tree")
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
	ctx, _ := spawnFixture(t)
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	for _, line := range []string{"git push origin HEAD:topic", "./magus agent harness install", "./magus config set --global key=log.format,value=json", "touch notes.txt"} {
		v := judgeUnloaded(ctx, deps, line)
		assert.Equal(t, "deny", v.Decision, line)
		assert.Contains(t, v.Reason, "That magus is older than the tree.", line)
		assert.Contains(t, v.Reason, "Rebuild it: `./magus run go-build .`.", line)
	}

	spawn := Judge(ctx, deps, Request{Input: claudeSpawnEnvelope, Host: "claude-code"})
	assert.Equal(t, "deny", spawn.Decision)
	assert.Contains(t, spawn.Reason, "a subagent spawn is denied")

	edit := Judge(ctx, deps, Request{Input: filepath.Join(root, "main.go"), IsPath: true, Host: "claude-code", Session: "s1"})
	assert.Equal(t, "deny", edit.Decision)
	assert.Contains(t, edit.Reason, "this file write is denied")

	for _, line := range []string{
		"ls", "git status", "git diff --stat", "./magus describe job x", "./magus run go-build .", "mv magus magus.old",
		"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .",
	} {
		v := judgeUnloaded(ctx, deps, line)
		assert.NotEqual(t, "deny", v.Decision, "%s: %s", line, v.Reason)
	}
}

// The remedy follows who is asking: the orchestrator or a person rebuilds, a leased worker
// asks the orchestrator to place a binary, and neither the worker's deny nor its
// bootstrap names a build.
func TestStaleBinaryRemedyFollowsTheCaller(t *testing.T) {
	ctx, _ := fleetFixture(t, fleetLeases()[0])
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	root1 := judgeUnloaded(ctx, deps, "touch notes.txt")
	assert.Contains(t, root1.Reason, "Rebuild it: `./magus run go-build .`")

	worker := Judge(ctx, deps, Request{Input: "touch notes.txt", Host: "claude-code", Session: "s1", Lease: "lease-a"})
	require.Equal(t, "deny", worker.Decision, worker.Reason)
	assert.Contains(t, worker.Reason, "one per base, so a worker never builds one: ask it to run `magus buzz hack/dev/bootstrap-worktree.buzz -- --job lease-a`, then retry.")
	assert.NotContains(t, worker.Reason, "go-build")

	for _, line := range []string{
		"./magus run go-build .",
		"GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .",
		"go build -o magus ./cmd/magus",
	} {
		v := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "s1", Lease: "lease-a"})
		assert.Equal(t, "deny", v.Decision, line)
		assert.NotContains(t, v.Reason, "Rebuild it", line)
	}
}

// The raw bootstrap in a binary-less checkout is advised through for the orchestrator and
// refused for a leased worker, who is told to ask for a placed binary.
func TestBootstrapIsTheOrchestratorsNotAWorkers(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()[0])
	// The bootstrap rule reads the directory the call runs in, which the fleet fixture leaves unset.
	ctx = WithLocation(ctx, hookLocation(ctx, Dependencies{}).cacheDir, root, root)
	ownCheckout(t, ctx, false)
	line := "GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache ."

	deps := strict(testDependencies())

	orchestrator := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "s1"})
	assert.Equal(t, "advise", orchestrator.Decision, orchestrator.Reason)

	worker := Judge(ctx, deps, Request{Input: line, Host: "claude-code", Session: "s1", Lease: "lease-a"})
	require.Equal(t, "deny", worker.Decision)
	assert.Contains(t, worker.Reason, "ask it to run `magus buzz hack/dev/bootstrap-worktree.buzz -- --job lease-a`")
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
	assert.Equal(t, "/work/tree/magus", commands[0].Binary)
	assert.Equal(t, "v0.5.0-rc.3-62-gcb43888d2", commands[0].BinaryVersion)
	assert.Equal(t, cwd, commands[0].Cwd)

	spawns := trailEvents(t, cacheDir, trail.KindAgentSpawn)
	require.Len(t, spawns, 1)
	assert.Equal(t, "/work/tree/magus", spawns[0].Binary)
	assert.Equal(t, "v0.5.0-rc.3-62-gcb43888d2", spawns[0].BinaryVersion)
	assert.Equal(t, "/Users/dev/repo", spawns[0].Cwd, "the spawn envelope's reported directory")
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
	ctx, _ := spawnFixture(t)
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
		v := Judge(ctx, deps, Request{Input: call, Host: "claude-code", Session: "s1"})
		assert.Equal(t, "deny", v.Decision, name)
		assert.Contains(t, v.Reason, "That magus is older than the tree.", name)
		assert.Contains(t, v.Reason, "Rebuild it: `./magus run go-build .`.", name)
		assert.Contains(t, v.Reason, "this call to the magus "+strings.Fields(name)[0]+" tool is denied", name)
	}

	for name, call := range map[string]string{
		"status":       mcpEnvelope(t, "status", map[string]any{}),
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

// A leased worker is told to ask the orchestrator for a binary, never to build one.
func TestStaleBinaryMCPDenyFollowsTheCaller(t *testing.T) {
	ctx, _ := fleetFixture(t, fleetLeases()[0])
	root := ownCheckout(t, ctx, true)
	declareGuardRule(t, root)
	deps := unloadedDepsFor(types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "%s", errMissingMember))

	worker := Judge(ctx, deps, Request{Input: mcpEnvelope(t, "client", map[string]any{"script": "x"}), Host: "claude-code", Session: "s1", Lease: "lease-a"})
	require.Equal(t, "deny", worker.Decision, worker.Reason)
	assert.Contains(t, worker.Reason, "ask it to run `magus buzz hack/dev/bootstrap-worktree.buzz -- --job lease-a`")
	assert.NotContains(t, worker.Reason, "go-build")

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
	} {
		assert.Equal(t, tc.want, recoveryLine(tc.line, DialectBash, tc.lease), "%s (lease %q)", tc.line, tc.lease)
	}
}
