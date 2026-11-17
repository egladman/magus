package notes

import (
	"path/filepath"
	"strings"
	"time"
)

// ScopedNote is a note plus where it lives, so a listing can never present a private
// note as if the team had it.
type ScopedNote struct {
	Note
	Scope string `json:"scope" yaml:"scope"`
	// Path is where the note lives, and it is here so a structured listing is enough to
	// ACT on: without it a consumer knows a note exists and cannot open it, which is the
	// difference between a report and an export. Same rendering as the notes service
	// (internal/handler/notes.Service.notePath), so the CLI and the RPC name one file the
	// same way rather than each having its own idea of where a note is.
	Path string `json:"path" yaml:"path"`
	// Modified shadows the embedded Note.Modified so an export can OMIT it instead of
	// emitting a zero timestamp. It is the file's mtime, which git rewrites on checkout, so
	// a committed export that carries it reports drift on every fresh clone with the
	// timestamp as the entire diff. A pointer is what makes "absent" expressible.
	Modified *time.Time `json:"modified,omitempty" yaml:"-"`
}

// NoteModified picks the mtime to serialize. Under --reproducible it is dropped rather than
// zeroed: a zero time is a value a reader can misread as "never touched", where an absent
// field says the export chose not to answer.
func NoteModified(n Note, reproducible bool) *time.Time {
	if reproducible {
		return nil
	}
	m := n.Modified
	return &m
}

// Locate renders where a note lives: workspace-relative for a shared note, absolute for a
// private one. The split follows the scope: a private store sits outside the workspace, so a
// relative path would be a lie about a file the reader still has to find.
//
// The path is the one the note was READ from (Note.Path), never one rebuilt from its
// name: a declared id is deliberately independent of the filename, so rebuilding names a
// file that stops existing the moment someone renames the note.
func Locate(root string, scope Scope, n Note) string {
	if scope != ScopeShared {
		return n.Path
	}
	return RelativeToRoot(root, n.Path)
}

// RelativeToRoot renders a path inside the workspace as workspace-relative, and leaves one
// outside it alone: a private store sits anywhere on disk, and a ../../.. walk out of the
// checkout names the file no more usefully than the absolute path does.
func RelativeToRoot(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.ToSlash(rel)
}

// StoreOutput names one declared store in a structured listing. Only DECLARED stores
// reach a listing at all (notesStores skips the rest), so a consumer reading this knows the
// location exists; an empty Notes list against a non-empty Stores list is the "declared and
// empty" case, which is a different fact from "this workspace has nowhere to put a note".
type StoreOutput struct {
	Scope string `json:"scope" yaml:"scope"`
	Path  string `json:"path" yaml:"path"`
}

type ListOutput struct {
	Stores []StoreOutput `json:"stores"`
	Notes  []ScopedNote  `json:"notes" jsonl:"primary"`
	Issues []Issue       `json:"issues"`
}
