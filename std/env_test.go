package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/sandbox"
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

func TestEnvReadDotenvHonorsSandbox(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(path, []byte("FOO=bar\n"), 0o644))

	// A policy with no matching rule denies the read.
	p := &sandbox.Policy{Workspace: t.TempDir()}
	ctx := sandbox.WithPolicy(context.Background(), p)

	_, err := EnvReadDotenv(ctx, path)
	assert.Error(t, err, "expected sandbox read denial")
}

func TestEnvLoadDotenvHonorsSandbox(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	require.NoError(t, os.WriteFile(path, []byte("MAGUS_TEST_LOAD_DOTENV_XYZ=bar\n"), 0o644))

	p := &sandbox.Policy{Workspace: t.TempDir()}
	ctx := sandbox.WithPolicy(context.Background(), p)

	err := EnvLoadDotenv(ctx, path)
	assert.Error(t, err, "expected sandbox read denial")
	_, ok := os.LookupEnv("MAGUS_TEST_LOAD_DOTENV_XYZ")
	assert.False(t, ok, "sandbox-denied dotenv must not be loaded into the process environment")
}
