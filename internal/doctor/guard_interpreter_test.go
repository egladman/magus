package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

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

	assert.Equal(t, types.DoctorAdvice, got.Status)
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

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "no ./magus built")
}

// onPath puts an executable magus on PATH and nothing else, so a case can state which
// binary the check is allowed to find.
func onPath(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	t.Setenv("PATH", dir)
}
