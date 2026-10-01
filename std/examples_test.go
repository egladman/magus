package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/spell"
)

// TestExamplesCompile walks every std/examples/**/*.buzz file and asserts each one
// parses AND type-checks. Catches syntax typos and, more often, an unhandled raise
// (BZZ1006) before the snippet ships in the docs, where cmd/magus-docs renders it
// under each method and a reader copies it.
//
// The examples are EMBEDDED snippets, not standalone scripts. Wrapping each one in a
// fun main() to satisfy upstream-strict parsing would put ceremony in front of the
// method the example exists to show. It compiles rather than evaluates: the calls
// touch fs, env and the network, which a unit test has no business doing.
func TestExamplesCompile(t *testing.T) {
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
		if err := compileExample(string(src), spell.ModuleDecls); err != nil {
			t.Errorf("%s: %v", path, err)
		}
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	t.Logf("compiled %d example file(s)", count)
}

// TestCompileExampleRefusesABrokenSnippet pins that compileExample reports a type
// error rather than passing every snippet, and that an embedded fragment with a
// top-level statement compiles.
func TestCompileExampleRefusesABrokenSnippet(t *testing.T) {
	if err := compileExample("import \"std\";\nstd\\print(\"ok\");\n", spell.ModuleDecls); err != nil {
		t.Fatalf("a valid embedded snippet must compile: %v", err)
	}
	if err := compileExample("import \"std\";\nfinal x: int = \"not an int\";\n", spell.ModuleDecls); err == nil {
		t.Fatal("a snippet that does not type-check must fail")
	}
}
