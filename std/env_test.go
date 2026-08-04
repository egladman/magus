package std

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/env"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvRequire(t *testing.T) {
	ctx := context.Background()

	t.Setenv("MAGUS_TEST_REQUIRE", "value")
	v, err := EnvRequire(ctx, "MAGUS_TEST_REQUIRE")
	require.NoError(t, err)
	assert.Equal(t, "value", v)

	// Set-but-empty satisfies the requirement (returns the empty value).
	t.Setenv("MAGUS_TEST_REQUIRE_EMPTY", "")
	v, err = EnvRequire(ctx, "MAGUS_TEST_REQUIRE_EMPTY")
	require.NoError(t, err)
	assert.Equal(t, "", v)

	// Unset raises.
	_, err = EnvRequire(ctx, "MAGUS_TEST_REQUIRE_MISSING_XYZ")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is not set")
}

// dotenvOutsidePolicy writes a .env file into a directory the returned context's
// sandbox policy does NOT allow reading, alongside one it does. It returns the
// denied path, the allowed path, and the policy-carrying context.
func dotenvOutsidePolicy(t *testing.T) (denied, allowed string, ctx context.Context) {
	t.Helper()
	// The ruleset compares symlink-resolved paths, and on macOS a temp dir sits
	// under a symlinked /var, so an unresolved rule would match nothing.
	evalDir := func() string {
		d, err := filepath.EvalSymlinks(t.TempDir())
		require.NoError(t, err)
		return d
	}
	allowedDir, deniedDir := evalDir(), evalDir()
	allowed = filepath.Join(allowedDir, ".env")
	denied = filepath.Join(deniedDir, ".env")
	require.NoError(t, os.WriteFile(allowed, []byte("IN_POLICY=yes\n"), 0o600))
	require.NoError(t, os.WriteFile(denied, []byte("MAGUS_TEST_DOTENV_LEAK=secret\n"), 0o600))

	// IN_POLICY is allowlisted so the positive load_dotenv case exercises the READ
	// gate rather than tripping the separate env-name gate first.
	p := &sandbox.Policy{
		Workspace: allowedDir,
		FS:        filesystem.Ruleset{Rules: []filesystem.Rule{{Path: allowedDir, Read: true}}},
		Env:       env.Allowlist{Allow: []string{"IN_POLICY"}},
	}
	return denied, allowed, sandbox.WithPolicy(context.Background(), p)
}

// TestEnvReadDotenvSandboxDenied pins that read_dotenv is bound by the same read
// policy as fs.read_file. It was not: it called os.ReadFile on the raw path, so a
// target could parse any file on the host that happened to hold KEY=VALUE lines.
func TestEnvReadDotenvSandboxDenied(t *testing.T) {
	denied, allowed, ctx := dotenvOutsidePolicy(t)

	got, err := EnvReadDotenv(ctx, denied)
	require.Error(t, err, "a read outside the policy must fail, not return the parsed file")
	assert.Nil(t, got)
	assert.True(t, errors.Is(err, types.PathReadDenied), "denial should carry MGS2001, like fs.read_file: %v", err)

	// The same call inside the policy still works, so the gate is the policy and
	// not a blanket refusal.
	got, err = EnvReadDotenv(ctx, allowed)
	require.NoError(t, err)
	assert.Equal(t, map[string]string{"IN_POLICY": "yes"}, got)
}

// TestEnvLoadDotenvSandboxDenied pins the worse half of the same hole: load_dotenv
// injected the parsed names into the process environment, so an ungated read did
// not merely return the contents, it published them to every later env.get and
// child process.
func TestEnvLoadDotenvSandboxDenied(t *testing.T) {
	denied, allowed, ctx := dotenvOutsidePolicy(t)

	err := EnvLoadDotenv(ctx, denied)
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.PathReadDenied), "denial should carry MGS2001: %v", err)
	_, present := os.LookupEnv("MAGUS_TEST_DOTENV_LEAK")
	assert.False(t, present, "a denied load must not reach the process environment")

	t.Setenv("IN_POLICY", "")
	require.NoError(t, os.Unsetenv("IN_POLICY"))
	require.NoError(t, EnvLoadDotenv(ctx, allowed))
	assert.Equal(t, "yes", os.Getenv("IN_POLICY"))
}

// TestEnvDotenvSandboxResolvesRelativePath pins that a relative path is resolved
// against the context cwd BEFORE the policy sees it. Checking the raw relative
// path would compare "../../.env" against absolute rules and wave it through.
func TestEnvDotenvSandboxResolvesRelativePath(t *testing.T) {
	denied, allowed, ctx := dotenvOutsidePolicy(t)
	ctx = WithCwd(ctx, filepath.Dir(allowed))

	_, err := EnvReadDotenv(ctx, ".env")
	require.NoError(t, err, "a relative path inside the cwd is allowed")

	rel, err := filepath.Rel(filepath.Dir(allowed), denied)
	require.NoError(t, err)
	_, err = EnvReadDotenv(ctx, rel)
	require.Error(t, err, "a relative path escaping the policy must be denied")
	assert.True(t, errors.Is(err, types.PathReadDenied), "denial should carry MGS2001: %v", err)
}
