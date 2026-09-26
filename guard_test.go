package magus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interp"
)

// The committed load is skipped only when no pending path can change what the root load
// registers, so each shape that can is pinned here.
func TestPendingTouchesLoad(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("import \"magus\";\n"), 0o644))
	sources := []interp.SourceFile{
		{Path: filepath.Join(root, "magusfile.buzz")},
		{Path: filepath.Join(root, "tools", "policy", "guard.buzz")},
	}
	sep := string(filepath.Separator)
	cases := []struct {
		name    string
		pending []string
		want    bool
	}{
		{"a file the load read", []string{filepath.Join(root, "tools", "policy", "guard.buzz")}, true},
		{"the root magusfile", []string{filepath.Join(root, "magusfile.buzz")}, true},
		{"the config that shapes the load", []string{filepath.Join(root, "magus.yaml")}, true},
		{"a new magusfiles source", []string{filepath.Join(root, "magusfiles", "a.buzz")}, true},
		{"an untracked directory holding a source", []string{filepath.Join(root, "tools") + sep}, true},
		{"an untracked magusfiles directory", []string{filepath.Join(root, "magusfiles") + sep}, true},
		{"a spell no load read", []string{filepath.Join(root, "spells", "go", "spell.buzz")}, false},
		{"an untracked directory elsewhere", []string{filepath.Join(root, "scratch") + sep}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, pendingTouchesLoad(root, newPendingSet(tc.pending), sources))
		})
	}
}

func TestPendingSetCoversFilesUnderAnUntrackedDirectory(t *testing.T) {
	root := t.TempDir()
	set := newPendingSet([]string{filepath.Join(root, "new") + string(filepath.Separator), filepath.Join(root, "a.buzz")})
	assert.True(t, set.covers(filepath.Join(root, "a.buzz")))
	assert.True(t, set.covers(filepath.Join(root, "new", "deep", "b.buzz")))
	assert.False(t, set.covers(filepath.Join(root, "newer.buzz")), "a sibling sharing the prefix is not under it")
	assert.False(t, set.covers(filepath.Join(root, "b.buzz")))
}
