package main

import (
	"path/filepath"
	"testing"

	store "github.com/egladman/magus/internal/notes"
)

// The scope decides the shape of the path, not just who can read the note, and the two cases
// are asserted together because getting them backwards is silent: an absolute path for a
// shared note still opens on the machine that produced the listing, and only stops working
// once someone else reads it.
func TestNotePathIsRelativeOnlyForSharedNotes(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")
	shared := notesStore{dir: filepath.Join(root, "notes"), scope: store.ScopeShared}
	private := notesStore{dir: filepath.Join(string(filepath.Separator), "home", "me", "vault"), scope: store.ScopePrivate}

	sharedNote := store.Note{Name: "why-buzz", Path: filepath.Join(root, "notes", "why-buzz.md")}
	if got, want := notePath(root, shared, sharedNote), "notes/why-buzz.md"; got != want {
		t.Errorf("shared note path = %q, want %q", got, want)
	}
	// A private store lives outside the workspace, so there is nothing for a relative path
	// to be relative TO; it stays absolute rather than becoming a ../.. walk.
	want := filepath.Join(string(filepath.Separator), "home", "me", "vault", "why-buzz.md")
	if got := notePath(root, private, store.Note{Name: "why-buzz", Path: want}); got != want {
		t.Errorf("private note path = %q, want %q", got, want)
	}
}

// The case the old derivation got wrong. A note that declares an id is identified by that id
// and NOT by its filename, so rebuilding the path from the name pointed the reader at a file
// that does not exist. The listing reports where the note actually is.
func TestNotePathReportsTheFileNotTheId(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")
	shared := notesStore{dir: filepath.Join(root, "notes"), scope: store.ScopeShared}
	n := store.Note{
		Name: "cache-pairing",
		ID:   "cache-pairing",
		Path: filepath.Join(root, "notes", "Archive", "Some Note.md"),
	}
	if got, want := notePath(root, shared, n), "notes/Archive/Some Note.md"; got != want {
		t.Errorf("renamed note path = %q, want %q", got, want)
	}
}
