package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathClean(t *testing.T) {
	got, err := PathClean(context.Background(), "a/b/../c/./d")
	require.NoError(t, err)
	assert.Equal(t, filepath.FromSlash("a/c/d"), got)
}

func TestPathRel(t *testing.T) {
	got, err := PathRel(context.Background(), "a/b", "a/b/c/d")
	require.NoError(t, err)
	assert.Equal(t, filepath.FromSlash("c/d"), got)
}

func TestPathIsAbs(t *testing.T) {
	abs, _ := PathAbs(context.Background(), "x")
	got, err := PathIsAbs(context.Background(), abs)
	require.NoError(t, err)
	assert.True(t, got, "is_abs(%q) should be true", abs)

	rel, _ := PathIsAbs(context.Background(), "x/y")
	assert.False(t, rel, "is_abs of a relative path should be false")
}

func TestPathExpandUser(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	ctx := context.Background()

	got, err := PathExpandUser(ctx, "~/proj")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "proj"), got)

	bare, _ := PathExpandUser(ctx, "~")
	assert.Equal(t, home, bare)

	// A non-~ path and another user's ~ are left untouched.
	for _, in := range []string{"/abs/path", "rel/path", "~other/x"} {
		got, _ := PathExpandUser(ctx, in)
		assert.Equal(t, in, got, "expand_user(%q) should be unchanged", in)
	}
}
