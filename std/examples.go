package std

import (
	"context"
	"regexp"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
)

// importLine matches the import statements an example opens with, so the session
// built for it declares exactly the modules it names.
var importLine = regexp.MustCompile(`(?m)^import\s+"([^"]+)"`)

// compileExample parses and type-checks src, one std/examples snippet, the way
// cmd/magus-docs presents it: as a fragment of a magusfile target body, so
// top-level control flow and positional arguments are legal.
//
// It compiles rather than evaluates, so it needs the DECLARATIONS only: the
// session carries Buzz's own stdlib for `import "std"` plus, from decls, the
// declarations of every magus module src imports, and no host bindings at all.
// decls is keyed by the bare module name (encoding/json -> json) and the result
// is registered under the IMPORT PATH src spells, the same split resolveImport
// makes. It is a parameter because the declarations live in internal/spell, whose
// own tests import this package.
func compileExample(src string, decls func(module string) (string, bool)) error {
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()

	buzzstd.Register(sess)
	for _, m := range importLine.FindAllStringSubmatch(src, -1) {
		importPath := m[1]
		if d, ok := decls(importPath[strings.LastIndex(importPath, "/")+1:]); ok {
			sess.SetModuleDecls(importPath, d)
		}
	}

	_, err := sess.Compile(src)
	return err
}
