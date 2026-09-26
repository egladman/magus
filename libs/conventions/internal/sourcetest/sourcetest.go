// Package sourcetest builds a module on disk for the tests that check a
// setting against the tree it names.
package sourcetest

import (
	"os"
	"path/filepath"
	"testing"
)

// Module writes a go.mod declaring module and one empty Go file per relative
// path, then makes the module root the working directory for the rest of t.
func Module(t *testing.T, module string, files ...string) string {
	t.Helper()
	root := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module "+module+"\n")
	for _, rel := range files {
		write(rel, "package "+filepath.Base(filepath.Dir(filepath.Join(root, rel)))+"\n")
	}
	t.Chdir(root)
	return root
}
