package image

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStripComponents(t *testing.T) {
	t.Parallel()
	got, err := stripComponents("rootfs/bin/sh", 0)
	require.NoError(t, err)
	assert.Equal(t, "rootfs/bin/sh", got)

	got, err = stripComponents("rootfs/bin/sh", 1)
	require.NoError(t, err)
	assert.Equal(t, "bin/sh", got)

	got, err = stripComponents("rootfs/bin/sh", 2)
	require.NoError(t, err)
	assert.Equal(t, "sh", got)

	_, err = stripComponents("rootfs/bin/sh", 3)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "removes entire path")

	_, err = stripComponents("rootfs", -1)
	require.Error(t, err)
}

func TestDirPlacementStrip(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, osWriteFile(filepath.Join(root, "bin", "hello"), []byte("hi"), 0o755))

	files, _, _, err := (DirPlacement{
		Source:      HostDir{Value: root},
		Destination: DestDir{Value: "/opt"},
		Strip:       1,
	}).expand(context.Background())
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "/opt/bin/hello", files[0].Destination)

	files, _, _, err = (DirPlacement{
		Source:      HostDir{Value: root},
		Destination: DestDir{Value: "/opt"},
		Strip:       0,
	}).expand(context.Background())
	require.NoError(t, err)
	require.Len(t, files, 1)
	base := filepath.Base(root)
	assert.Equal(t, path.Join("/opt", base, "bin", "hello"), files[0].Destination)
}

func TestDirPlacementEmptyDirs(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "empty"), 0o755))
	require.NoError(t, osWriteFile(filepath.Join(root, "bin", "hello"), []byte("hi"), 0o755))

	files, _, dirs, err := (DirPlacement{
		Source:      HostDir{Value: root},
		Destination: DestDir{Value: "/opt"},
		Strip:       1,
		EmptyDirs:   true,
	}).expand(context.Background())
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "/opt/bin/hello", files[0].Destination)
	require.Len(t, dirs, 2) // bin (parent of hello) and empty
	got := map[string]bool{}
	for _, d := range dirs {
		got[d.Destination] = true
	}
	assert.True(t, got["/opt/bin"])
	assert.True(t, got["/opt/empty"])
}

// osWriteFile creates parent dirs then writes path — test helper, not production.
func osWriteFile(path string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, body, mode)
}
