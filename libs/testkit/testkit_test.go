package testkit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProjectsPreservesOrder pins the one property callers rely on: shard and score
// tests assert against positions, so the order in is the order out.
func TestProjectsPreservesOrder(t *testing.T) {
	got := Projects("a", "b", "c")
	require.Len(t, got, 3)
	assert.Equal(t, "a", got[0].Path)
	assert.Equal(t, "b", got[1].Path)
	assert.Equal(t, "c", got[2].Path)
}

// TestProjectsEmpty covers the degenerate call, which several table tests make.
func TestProjectsEmpty(t *testing.T) {
	assert.Empty(t, Projects())
}

func TestWorkspaceWriteCreatesParents(t *testing.T) {
	w := NewWorkspace(t)
	abs := w.Write("deep/nested/file.txt", "body")

	assert.Equal(t, filepath.Join(w.Root(), "deep/nested/file.txt"), abs)
	body, err := os.ReadFile(abs)
	require.NoError(t, err)
	assert.Equal(t, "body", string(body))
}

func TestWorkspaceMagusfile(t *testing.T) {
	w := NewWorkspace(t)
	abs := w.Magusfile("magus\\project({});")

	assert.Equal(t, filepath.Join(w.Root(), "magusfile.buzz"), abs)
	body, err := os.ReadFile(abs)
	require.NoError(t, err)
	assert.Contains(t, string(body), "magus\\project")
}

func TestWorkspaceWriteTree(t *testing.T) {
	w := NewWorkspace(t)
	w.WriteTree(map[string]string{
		"a.go":       "package a",
		"sub/b.go":   "package b",
		"sub/c/d.md": "# d",
	})

	for rel, want := range map[string]string{
		"a.go": "package a", "sub/b.go": "package b", "sub/c/d.md": "# d",
	} {
		body, err := os.ReadFile(w.Path(rel))
		require.NoError(t, err, rel)
		assert.Equal(t, want, string(body), rel)
	}
}

// TestWorkspaceRefusesAnEscapingPath is the safety property. A fixture that can write
// outside its own root is a test that can damage the checkout it runs in, and the
// failure would land somewhere nobody is looking for it.
func TestWorkspaceRefusesAnEscapingPath(t *testing.T) {
	for _, rel := range []string{"../escaped.txt", "sub/../../escaped.txt"} {
		t.Run(rel, func(t *testing.T) {
			fake := &fatalRecorder{TB: t}
			w := &Workspace{tb: fake, root: t.TempDir()}
			w.Write(rel, "nope")
			assert.True(t, fake.failed, "writing %q outside the root must fail the test", rel)
		})
	}
}

// fatalRecorder stands in for the caller's testing.TB so a test can assert that a
// fixture REFUSES something, without the refusal ending the test that is checking it.
type fatalRecorder struct {
	testing.TB
	failed bool
}

func (f *fatalRecorder) Fatalf(string, ...any) { f.failed = true }
func (f *fatalRecorder) Helper()               {}
