package notes

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLocateIsRelativeOnlyForSharedNotes(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")

	sharedNote := Note{Name: "why-buzz", Path: filepath.Join(root, "notes", "why-buzz.md")}
	if got, want := Locate(root, ScopeShared, sharedNote), "notes/why-buzz.md"; got != want {
		t.Errorf("shared note path = %q, want %q", got, want)
	}
	// A private store lives outside the workspace, so there is nothing for a relative path
	// to be relative TO; it stays absolute rather than becoming a ../.. walk.
	want := filepath.Join(string(filepath.Separator), "home", "me", "vault", "why-buzz.md")
	if got := Locate(root, ScopePrivate, Note{Name: "why-buzz", Path: want}); got != want {
		t.Errorf("private note path = %q, want %q", got, want)
	}
}

// The case the old derivation got wrong. A note that declares an id is identified by that id
// and NOT by its filename, so rebuilding the path from the name pointed the reader at a file
// that does not exist. The listing reports where the note actually is.
func TestLocateReportsTheFileNotTheId(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")
	n := Note{
		Name: "cache-pairing",
		ID:   "cache-pairing",
		Path: filepath.Join(root, "notes", "Archive", "Some Note.md"),
	}
	if got, want := Locate(root, ScopeShared, n), "notes/Archive/Some Note.md"; got != want {
		t.Errorf("renamed note path = %q, want %q", got, want)
	}
}

func TestRelativeToRootKeepsAnOutsidePathAbsolute(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repos", "magus")

	assert.Equal(t, "cmd/magus/diff.go", RelativeToRoot(root, filepath.Join(root, "cmd", "magus", "diff.go")))
	assert.Equal(t, ".", RelativeToRoot(root, root))

	outside := filepath.Join(string(filepath.Separator), "repos", "other", "x.go")
	assert.Equal(t, outside, RelativeToRoot(root, outside))
}
