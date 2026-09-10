package std

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/spellruntime"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
)

// importLine matches the import statements an example opens with, so the session
// built for it declares exactly the modules it names.
var importLine = regexp.MustCompile(`(?m)^import\s+"([^"]+)"`)

// TestExamplesCompile walks every std/examples/**/*.buzz file and asserts each one
// parses AND type-checks. Catches syntax typos and, more often, an unhandled raise
// (BZZ1006) before the snippet ships in the docs, where cmd/magus-docs renders it
// under each method and a reader copies it.
//
// The examples are EMBEDDED snippets, not standalone scripts: they are read as
// fragments of a magusfile target body, so top-level control flow and positional
// arguments are legal, exactly as the magusfile engine and `magus buzz --embedded`
// parse them. Wrapping each one in a fun main() to satisfy upstream-strict parsing
// would put ceremony in front of the method the example exists to show.
//
// It compiles rather than evaluates: the calls touch fs, env and the network, which
// a unit test has no business doing. Compiling needs the DECLARATIONS only, so each
// example gets a session carrying the generated decls for the modules it imports and
// no host bindings at all.
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
		if _, err := exampleSession(t, string(src)).Compile(string(src)); err != nil {
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

// exampleSession builds the checking session for one example: Buzz's own stdlib for
// `import "std"`, plus the generated declarations of every magus module the source
// imports. Declarations are registered under the IMPORT PATH the source spells while
// the generated file is named for the bare module (encoding/json -> json.buzz), which
// is the same split resolveImport makes.
func exampleSession(t *testing.T, src string) *buzz.Session {
	t.Helper()
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	t.Cleanup(func() { _ = sess.Close() })
	buzzstd.Register(sess)
	for _, m := range importLine.FindAllStringSubmatch(src, -1) {
		importPath := m[1]
		name := importPath[strings.LastIndex(importPath, "/")+1:]
		if decls, ok := spellruntime.ModuleDecls(name); ok {
			sess.SetModuleDecls(importPath, decls)
		}
	}
	return sess
}
