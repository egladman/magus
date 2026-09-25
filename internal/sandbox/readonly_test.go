package sandbox

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/sandbox/filesystem"
)

// With the sandbox off, read-only still refuses every write and exec, and leaves reads
// and the environment as they were.
func TestReadOnlyOfNoPolicy(t *testing.T) {
	t.Setenv("MAGUS_READONLY_TEST_SECRET", "x")
	p := ReadOnly(nil)
	ctx := t.Context()

	assert.NoError(t, p.CheckRead(ctx, "/etc/hosts"))
	assert.NoError(t, p.CheckRead(ctx, t.TempDir()))
	assert.ErrorIs(t, p.CheckWrite(ctx, filepath.Join(t.TempDir(), "out")), filesystem.ErrDenied)
	assert.ErrorIs(t, p.CheckExec(ctx, "/bin/sh"), filesystem.ErrDenied)
	assert.True(t, p.AllowsEnv("MAGUS_READONLY_TEST_SECRET"), "read-only hides no variable the sandbox would not")
}

// Over a workspace policy, read-only keeps its reads and drops its writes and execs,
// and the policy it was derived from keeps its grants.
func TestReadOnlyOfAWorkspacePolicy(t *testing.T) {
	ws := t.TempDir()
	base := BuildPolicy(PolicyOptions{Workspace: ws})
	p := ReadOnly(base)
	ctx := t.Context()

	inside := filepath.Join(ws, "file")
	assert.NoError(t, p.CheckRead(ctx, inside))
	assert.ErrorIs(t, p.CheckRead(ctx, "/magus-readonly-test/outside"), filesystem.ErrDenied, "reads stay as narrow as the workspace made them")
	assert.ErrorIs(t, p.CheckWrite(ctx, inside), filesystem.ErrDenied)
	assert.ErrorIs(t, p.CheckWrite(ctx, filepath.Join(os.TempDir(), "x")), filesystem.ErrDenied)
	assert.ErrorIs(t, p.CheckExec(ctx, filepath.Join(ws, "bin")), filesystem.ErrDenied)
	for _, r := range p.FS.Rules {
		assert.False(t, r.Write || r.Exec, "rule %s still grants write or exec to the kernel layer", r.Path)
	}

	require.NoError(t, base.CheckWrite(ctx, inside), "deriving read-only must not narrow the workspace policy")
	assert.False(t, base.ReadOnly)
}

func TestReadOnlyKernelRulesGrantNoWriteButTheNullDevice(t *testing.T) {
	for _, r := range readOnlyKernelRules() {
		if r.Write {
			assert.Equal(t, os.DevNull, r.Path)
		}
	}
}
