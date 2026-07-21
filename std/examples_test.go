package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// TestExamplesParse walks every std/examples/**/*.buzz file and asserts each one
// parses cleanly. Catches syntax typos in per-method example snippets before
// they ship in the docs (cmd/magus-docs renders them under each method).
//
// It does not evaluate the examples: many call magus host modules that need a
// real session with host bindings, and some legitimately touch fs/env/network
// that are inappropriate for unit-test execution.
func TestExamplesParse(t *testing.T) {
	root := "examples"
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skip("no std/examples/ directory yet")
	}

	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".buzz") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			t.Errorf("%s: read: %v", path, err)
			return nil
		}
		// ParseEmbedded matches how magusfiles and the REPL parse (top-level
		// statements and positional args allowed). The strict Parse mode is for
		// upstream Buzz script conformance, which is not what these snippets are.
		if _, err := buzz.ParseEmbedded(string(src)); err != nil {
			t.Errorf("%s: parse: %v", path, err)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	t.Logf("parsed %d example file(s)", count)
}
