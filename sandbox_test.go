package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
)

// A workspace with no magus.yaml is a workspace that never asked for anything, so
// defaults are the right answer and no error.
func TestLoadWorkspaceConfigFallsBackWhenTheFileIsAbsent(t *testing.T) {
	t.Parallel()

	cfg, err := loadWorkspaceConfig(t.TempDir())
	require.NoError(t, err)
	assert.False(t, cfg.Sandbox.Mode.Enabled(), "defaults do not enable the sandbox")
}

// The failure this exists to prevent: a magus.yaml that asked for sandboxing but does not
// parse used to collapse into Defaults(), which disables it, so the workspace joined the
// server's union unsandboxed and nothing said so.
func TestLoadWorkspaceConfigRefusesAMalformedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("sandbox:\n  mode: [required\n"), 0o644))

	_, err := loadWorkspaceConfig(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "magus.yaml")
}

func TestApplyUnionSandboxReportsAMalformedWorkspaceConfig(t *testing.T) {
	t.Parallel()

	for _, yaml := range []string{
		"sandbox:\n  mode: [required\n",
		"sandbox:\n  mode: on\n",
		"sandbox:\n  enabled: true\n",
		"sandbox:\n  mode: best-effort\n  env:\n    passthrough: [\"GO*\"]\n",
	} {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(yaml), 0o644))
		assert.Error(t, ApplyUnionSandbox(context.Background(), []string{root}), yaml)
	}
}

// TestApplyUnionSandboxIsInertWithoutAnOptIn: the server applies a kernel policy
// only when some workspace asked for one. No roots, or roots that never enable
// sandboxing, must leave the process unconfined: applying a policy nobody
// requested would break every other workspace the server serves.
func TestApplyUnionSandboxIsInertWithoutAnOptIn(t *testing.T) {
	ctx := context.Background()

	assert.NoError(t, ApplyUnionSandbox(ctx, nil))
	assert.NoError(t, ApplyUnionSandbox(ctx, []string{}))

	root := writeWorkspace(t, map[string]string{"magusfile.buzz": ""})
	assert.NoError(t, ApplyUnionSandbox(ctx, []string{root}),
		"a workspace with no sandbox block requests nothing")
}

// A server holding a workspace that requires the sandbox refuses to start where the
// kernel cannot enforce it, rather than serving it behind binding checks alone.
func TestApplyUnionSandboxRefusesAnUnenforceableRequiredWorkspace(t *testing.T) {
	if sandbox.Supported() {
		t.Skip("landlock is supported here; the union would be applied to the test process")
	}
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz": "",
		"magus.yaml":     "sandbox:\n  mode: required\n",
	})
	err := ApplyUnionSandbox(context.Background(), []string{root})
	require.ErrorIs(t, err, types.SandboxRequired)
	assert.ErrorContains(t, err, root)
}
