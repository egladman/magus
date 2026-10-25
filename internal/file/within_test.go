package file

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWithinFollowsSymlinksAndRefusesAnEscape(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "sub", "in.txt"), nil, 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "out.txt"), nil, 0o644))
	for name, target := range map[string]string{
		"in-link":  filepath.Join(root, "sub", "in.txt"),
		"out-link": filepath.Join(outside, "out.txt"),
	} {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}

	got, err := Within(root, "in-link")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "sub", "in.txt"), got)

	got, err = Within(root, filepath.Join(root, "sub", "in.txt"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "sub", "in.txt"), got)

	for _, path := range []string{"out-link", fmt.Sprintf("../%s/out.txt", filepath.Base(outside)), filepath.Join(outside, "out.txt")} {
		_, err = Within(root, path)
		require.ErrorIs(t, err, ErrOutsideRoot, path)
	}

	_, err = Within(root, "missing")
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrOutsideRoot)
}
