package std

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/spell"
)

// TestExamplesCompile parses and type-checks every std/examples/**/*.buzz file.
// It catches syntax typos and unhandled raises (BZZ1006) before the docs ship them.
//
// Each example is a whole program with its own fun main, so it needs no wrapper.
// It compiles rather than evaluates: the calls touch fs, env and the network.
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
		if err := compileExample(string(src), exampleDecls); err != nil {
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

// exampleDecls is spell.ModuleDecls with magus\Context and the other shapes a
// target receives, since the image examples are whole magusfiles.
func exampleDecls(module string) (string, bool) {
	if module == "magus" {
		return spell.MagusDeclSource(), true
	}
	return spell.ModuleDecls(module)
}
