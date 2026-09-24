// Package testkit builds the fixtures a magus test needs: a throwaway workspace on
// disk, the domain values a test asserts against, and an environment built from an
// allowlist (Environ, Isolate, Main), so a variable nobody listed never reaches a test.
// A package whose tests need more names states them as Main's keep list.
//
// It is public because the audience is not only magus's own tests. A program that
// embeds magus as a library (see the magus SDK) has the same problem magus does:
// exercising workspace-shaped behavior requires a workspace-shaped fixture, and
// hand-rolling one per package is how a tree ends up with four spellings of the same
// helper. magus had exactly that before this package existed.
//
// It lives in the main module rather than carrying its own go.mod, because it builds
// magus's own domain types and a separate module would make magus and testkit require
// each other.
//
// Every constructor takes a testing.TB and fails the test on error rather than
// returning one. A fixture that cannot be built is not a condition a test handles; it
// is the test being unable to start, and threading an error through every call site
// buys nothing but noise. Environ is the exception: a generator outside any test calls
// it, so it returns its error.
package testkit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
)

// Projects builds one project per path, in the order given.
//
// The zero value elsewhere in the struct is deliberate: a test that needs targets,
// sources or dependencies sets them on the returned projects, and a test that only
// needs identity says so by not.
func Projects(paths ...string) []*types.Project {
	out := make([]*types.Project, len(paths))
	for i, p := range paths {
		out[i] = &types.Project{Path: p}
	}
	return out
}

// Workspace is a throwaway directory tree a test writes files into.
//
// The root is a per-test temporary directory, removed when the test ends, so a test
// never has to clean up and two tests never share a tree.
type Workspace struct {
	tb   testing.TB
	root string
}

// NewWorkspace returns an empty workspace rooted at a temporary directory.
func NewWorkspace(tb testing.TB) *Workspace {
	tb.Helper()
	return &Workspace{tb: tb, root: tb.TempDir()}
}

// Root returns the absolute path of the workspace root.
func (w *Workspace) Root() string { return w.root }

// Path joins rel onto the root and returns the absolute path. It creates nothing.
func (w *Workspace) Path(rel ...string) string {
	return filepath.Join(append([]string{w.root}, rel...)...)
}

// Write creates rel with body, making parent directories as needed, and returns the
// absolute path it wrote.
//
// rel must stay inside the workspace. A fixture that can write outside its own root is
// a test that can damage the checkout it runs in.
func (w *Workspace) Write(rel, body string) string {
	w.tb.Helper()
	abs := w.Path(rel)
	inside, err := filepath.Rel(w.root, abs)
	if err != nil || inside == ".." || filepath.IsAbs(rel) ||
		len(inside) > 2 && inside[:3] == ".."+string(filepath.Separator) {
		w.tb.Fatalf("testkit: %q escapes the workspace root", rel)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		w.tb.Fatalf("testkit: create dir for %q: %v", rel, err)
	}
	if err := os.WriteFile(abs, []byte(body), 0o644); err != nil {
		w.tb.Fatalf("testkit: write %q: %v", rel, err)
	}
	return abs
}

// WriteTree writes every file in files, keyed by workspace-relative path.
//
// Map iteration order is unspecified and deliberately not compensated for: a fixture
// whose correctness depends on the order its files were written is a fixture hiding a
// dependency the test should state.
func (w *Workspace) WriteTree(files map[string]string) {
	w.tb.Helper()
	for rel, body := range files {
		w.Write(rel, body)
	}
}

// Magusfile writes body as the workspace's root magusfile and returns its path.
//
// Named for the file rather than for the act, because that is what a reader of the test
// is looking for when they scan it.
func (w *Workspace) Magusfile(body string) string {
	w.tb.Helper()
	return w.Write("magusfile.buzz", body)
}
