package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// The interpreter probe waits as long as a hook would, the budget agent.ProbeTimeout names.
// A fixed 3s of its own failed a stand-in shell script on a loaded machine, a hang that was
// only the machine; and it ignored the one override tests use.
func TestInterpreterSkewWaitsTheHookBudget(t *testing.T) {
	slow := testkit.NewWorkspace(t).WriteExecutable("magus", "#!/bin/sh\nsleep 1\necho 'magus v0.5.0 (abc1234) built 2026-01-01'\n")

	_, skewed := interpreterSkew(t.Context(), slow, "v0.5.0")
	assert.False(t, skewed, "an answer inside the hook budget is the same build")

	got, skewed := interpreterSkew(agent.ContextWithProbeTimeout(t.Context(), 100*time.Millisecond), slow, "v0.5.0")
	require.True(t, skewed, "past the budget the override sets")
	assert.Contains(t, got.Message, "did not answer `version`")
}

// A magus on PATH does not cover a hook command that spells ./magus: the host runs the
// string as written. This answered "hook would run <PATH magus>", which was untrue, until
// the check read the config instead of re-deriving the order the SCRIPTS resolve in.
//
// Advice rather than fail: a build machine runs no hooks and has no ./magus yet, so the
// state is normal there and only the old ANSWER was wrong.
func TestCheckGuardBinaryAdvisesWhenAWiredCommandNamesAMissingOwnBinary(t *testing.T) {
	w := testkit.NewWorkspace(t)
	writeCheckpointHarness(t, w, `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"./magus buzz -s magus-command.buzz"}]}]}}`)
	require.NoError(t, os.Remove(w.Path("magus")))
	onPath(t)

	got := (&runner{ws: rootedWorkspace{root: w.Root(), harnesses: []string{"test-host"}}}).checkGuardBinary()

	assert.Equal(t, types.CheckAdvice, got.Status)
	assert.Contains(t, got.Message, "cannot launch in a live session")
	assert.Contains(t, got.Details[0], filepath.FromSlash("host/hooks.json"))
}

// The PATH fallback stays healthy for a config that names a bare `magus`, which a magus on
// PATH does satisfy. Without this the fix would turn every PATH-only checkout red.
func TestCheckGuardBinaryPassesWhenAWiredCommandNamesTheBareBinary(t *testing.T) {
	w := testkit.NewWorkspace(t)
	writeCheckpointHarness(t, w, `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"magus buzz -s magus-command.buzz"}]}]}}`)
	require.NoError(t, os.Remove(w.Path("magus")))
	onPath(t)

	got := (&runner{ws: rootedWorkspace{root: w.Root(), harnesses: []string{"test-host"}}}).checkGuardBinary()

	assert.Equal(t, types.CheckOK, got.Status)
	assert.Contains(t, got.Message, "no ./magus built")
}

// The interpreter, not the verdict binary: an installed v0.4.3 ahead of the checkout's
// build ran every hook in a session and rendered no verdict, while this check said
// "hook would run" it and stopped there.
func TestCheckGuardBinaryFailsWhenTheHookInterpreterIsAnotherBuild(t *testing.T) {
	bin := testkit.NewWorkspace(t)
	stale := versionedMagus(bin, "v0.4.3")
	t.Setenv("PATH", bin.Root())
	r := &runner{ws: rootedWorkspace{root: t.TempDir()}, opts: options{serverInfo: &ServerInfo{ClientVersion: "v0.5.0"}}}

	got := r.checkGuardBinary()

	assert.Equal(t, types.CheckFail, got.Status)
	assert.Contains(t, got.Message, stale+" (v0.4.3)", "names the binary a hook runs, and its build")
	assert.Contains(t, got.Message, "this magus is v0.5.0", "and the build it should have been")

	r.opts.serverInfo.ClientVersion = "v0.4.3"
	assert.Equal(t, types.CheckOK, r.checkGuardBinary().Status, "the same build passes")
}

// A binary that cannot say which build it is cannot run the glue either.
func TestCheckGuardBinaryFailsWhenTheHookInterpreterPrintsNoVersion(t *testing.T) {
	w := testkit.NewWorkspace(t)
	bin := w.WriteExecutable("magus", "#!/bin/sh\nexit 3\n")
	withoutPathMagus(t)
	r := &runner{ws: rootedWorkspace{root: w.Root()}, opts: options{serverInfo: &ServerInfo{ClientVersion: "v0.5.0"}}}

	got := r.checkGuardBinary()

	assert.Equal(t, types.CheckFail, got.Status)
	assert.Contains(t, got.Message, "did not answer `version`")
	assert.Contains(t, got.Message, bin)
}

// writeInterpreter writes a stand-in hook interpreter and execs it once, outside any probe
// budget. macOS assesses an executable on its first exec: at a load average of 40 that
// took 1 to 17s against 6 to 38ms for the next exec of the same file, so a budget timed
// from the first exec measured the assessment.
func writeInterpreter(t *testing.T, path, script string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(script), 0o755))
	_ = exec.CommandContext(t.Context(), path, "version").Run()
}

// onPath puts an executable magus on PATH and nothing else, so a case can state which
// binary the check is allowed to find.
func onPath(t *testing.T) {
	t.Helper()
	dir := testkit.NewWorkspace(t)
	dir.WriteExecutable("magus", "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", dir.Root())
}

// versionedMagus writes an executable magus into w that prints version as `magus version` does.
func versionedMagus(w *testkit.Workspace, version string) string {
	return w.WriteExecutable("magus", "#!/bin/sh\necho 'magus "+version+" (abc1234) built 2026-01-01'\n")
}

// A call from console/ or docs/, each with a magusfile.buzz and no binary, runs the root's
// ./magus: the nearest magus.yaml is the root, not the nearest magusfile.
func TestHookBinaryFromANestedProjectIsTheRootsBinary(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	bin := versionedMagus(w, "v0.5.0")
	w.Write("magus.yaml", "{}\n")
	nested := w.Path("console", "src")
	require.NoError(t, os.MkdirAll(nested, 0o755))
	w.Write("console/magusfile.buzz", "\n")
	withoutPathMagus(t)

	assert.Equal(t, bin, hookBinaryFrom(nested))
	assert.Equal(t, bin, hookBinaryFrom(filepath.Join(root, "console")))
	assert.Equal(t, bin, hookBinaryFrom(root))
}

// A worktree nested under another checkout never resolves the parent's binary: its own
// magus.yaml is the nearest.
func TestHookBinaryFromAWorktreeNeverClimbsIntoTheParent(t *testing.T) {
	parent := testkit.NewWorkspace(t)
	versionedMagus(parent, "v0.5.0")
	parent.Write("magus.yaml", "{}\n")
	parent.Write(".claude/worktrees/job/magus.yaml", "{}\n")
	worktree := parent.Path(".claude", "worktrees", "job")
	require.NoError(t, os.MkdirAll(filepath.Join(worktree, "console"), 0o755))
	withoutPathMagus(t)

	assert.Equal(t, "", hookBinaryFrom(filepath.Join(worktree, "console")))

	pathBin := versionedMagus(testkit.NewWorkspace(t), "v0.4.3")
	t.Setenv("PATH", filepath.Dir(pathBin))
	assert.Equal(t, pathBin, hookBinaryFrom(filepath.Join(worktree, "console")), "falls to PATH, not to the parent's binary")
}

// A linked worktree (.git is a file) with no ./magus is an error, whatever PATH holds.
func TestCheckGuardBinaryFailsForAWorktreeWithoutABinary(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Write(".git", "gitdir: /elsewhere\n")
	require.NoError(t, os.MkdirAll(w.Path("cmd", "magus"), 0o755))
	pathBin := versionedMagus(testkit.NewWorkspace(t), "v0.5.0")
	t.Setenv("PATH", filepath.Dir(pathBin))
	r := &runner{ws: rootedWorkspace{root: root}, opts: options{serverInfo: &ServerInfo{ClientVersion: "v0.5.0"}}}

	got := r.checkGuardBinaryEverywhere(nil)

	assert.Equal(t, types.Check{
		Name:    guardBinaryCheck,
		Status:  types.CheckFail,
		Message: "this worktree has no ./magus",
		Details: []string{
			"its guard hooks judge with the magus on PATH, which may not load this tree",
			"the main session builds one ./magus per base and places a copy here",
			"place it: <main checkout>/magus buzz hack/dev/bootstrap-worktree.buzz -- --job <id> --from <main checkout>",
			"on your own: build one with " + hint.Run.With("build", ".") + ", or bootstrap it with the command the guard prints",
		},
	}, got)

	require.NoError(t, os.Remove(filepath.Join(root, ".git")))
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	main := r.checkGuardBinaryEverywhere(nil)
	assert.NotContains(t, main.Message, "this worktree", "a main checkout with no ./magus is the ordinary state")
}

// The check resolves the hook from every project directory: one whose hook would run a
// different build than the one that loaded the tree fails it, naming the directory.
func TestCheckGuardBinaryFailsWhenAProjectResolvesAnotherBuild(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	versionedMagus(w, "v0.5.0")
	w.Write("magus.yaml", "{}\n")
	strayWS := testkit.NewWorkspace(t)
	strayWS.Write("magus.yaml", "{}\n")
	stray := strayWS.Root()
	stale := versionedMagus(testkit.NewWorkspace(t), "v0.4.3")
	t.Setenv("PATH", filepath.Dir(stale))
	r := &runner{ws: rootedWorkspace{root: root}, opts: options{serverInfo: &ServerInfo{ClientVersion: "v0.5.0"}}}
	projects := []*types.Project{{Path: ".", Dir: root}, {Path: "stray", Dir: stray}}

	got := r.checkGuardBinaryEverywhere(projects)

	assert.Equal(t, types.CheckFail, got.Status)
	assert.Contains(t, got.Message, stale+" (v0.4.3)")
	assert.Equal(t, "resolved from the project directory "+stray, got.Details[0])

	assert.Equal(t, types.CheckOK, r.checkGuardBinaryEverywhere(projects[:1]).Status, "the root alone passes")
}

// No magus for a project directory is a failure, not a pass: nothing judges calls there.
func TestCheckGuardBinaryFailsWhenAProjectResolvesNoBinary(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	versionedMagus(w, "v0.5.0")
	w.Write("magus.yaml", "{}\n")
	strayWS := testkit.NewWorkspace(t)
	strayWS.Write("magus.yaml", "{}\n")
	stray := strayWS.Root()
	withoutPathMagus(t)
	r := &runner{ws: rootedWorkspace{root: root}, opts: options{serverInfo: &ServerInfo{ClientVersion: "v0.5.0"}}}

	got := r.checkGuardBinaryEverywhere([]*types.Project{{Path: "stray", Dir: stray}})

	assert.Equal(t, types.CheckFail, got.Status)
	assert.Contains(t, got.Message, "no magus resolves for a hook run in "+stray)
}
