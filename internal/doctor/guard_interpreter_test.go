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
	"github.com/egladman/magus/types"
)

// The interpreter probe waits as long as a hook would, the budget agent.ProbeTimeout names.
// A fixed 3s of its own failed a stand-in shell script on a loaded machine, a hang that was
// only the machine; and it ignored the one override tests use.
func TestInterpreterSkewWaitsTheHookBudget(t *testing.T) {
	slow := filepath.Join(t.TempDir(), "magus")
	writeInterpreter(t, slow, "#!/bin/sh\nsleep 1\necho 'magus v0.5.0 (abc1234) built 2026-01-01'\n")

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
	root := t.TempDir()
	writeCheckpointHarness(t, root, `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"./magus buzz -s magus-command.buzz"}]}]}}`)
	require.NoError(t, os.Remove(filepath.Join(root, "magus")))
	onPath(t)

	got := (&runner{ws: rootStubWorkspace{root: root, harnesses: []string{"test-host"}}}).checkGuardBinary()

	assert.Equal(t, types.CheckAdvice, got.Status)
	assert.Contains(t, got.Message, "cannot launch in a live session")
	assert.Contains(t, got.Details[0], filepath.FromSlash("host/hooks.json"))
}

// The PATH fallback stays healthy for a config that names a bare `magus`, which a magus on
// PATH does satisfy. Without this the fix would turn every PATH-only checkout red.
func TestCheckGuardBinaryPassesWhenAWiredCommandNamesTheBareBinary(t *testing.T) {
	root := t.TempDir()
	writeCheckpointHarness(t, root, `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"magus buzz -s magus-command.buzz"}]}]}}`)
	require.NoError(t, os.Remove(filepath.Join(root, "magus")))
	onPath(t)

	got := (&runner{ws: rootStubWorkspace{root: root, harnesses: []string{"test-host"}}}).checkGuardBinary()

	assert.Equal(t, types.CheckOK, got.Status)
	assert.Contains(t, got.Message, "no ./magus built")
}

// The interpreter, not the verdict binary: an installed v0.4.3 ahead of the checkout's
// build ran every hook in a session and rendered no verdict, while this check said
// "hook would run" it and stopped there.
func TestCheckGuardBinaryFailsWhenTheHookInterpreterIsAnotherBuild(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "magus")
	writeInterpreter(t, stale, "#!/bin/sh\necho 'magus v0.4.3 (abc1234) built 2026-01-01'\n")
	t.Setenv("PATH", dir)
	r := &runner{ws: rootStubWorkspace{root: t.TempDir()}, opts: options{serverInfo: &ServerInfo{ClientVersion: "v0.5.0"}}}

	got := r.checkGuardBinary()

	assert.Equal(t, types.CheckFail, got.Status)
	assert.Contains(t, got.Message, stale+" (v0.4.3)", "names the binary a hook runs, and its build")
	assert.Contains(t, got.Message, "this magus is v0.5.0", "and the build it should have been")

	r.opts.serverInfo.ClientVersion = "v0.4.3"
	assert.Equal(t, types.CheckOK, r.checkGuardBinary().Status, "the same build passes")
}

// A binary that cannot say which build it is cannot run the glue either.
func TestCheckGuardBinaryFailsWhenTheHookInterpreterPrintsNoVersion(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "magus")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nexit 3\n"), 0o755))
	withoutPathMagus(t)
	r := &runner{ws: rootStubWorkspace{root: root}, opts: options{serverInfo: &ServerInfo{ClientVersion: "v0.5.0"}}}

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
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("PATH", dir)
}
