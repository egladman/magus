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
// session carries Buzz's own stdlib for `import "std"`, the Buzz-implemented
// modules (magus/modeltiers and the rest) as the engine registers them, and, from
// decls, the declarations of every magus module src imports. No host bindings.
// decls is keyed by the bare module name (encoding/json -> json) and the result
// is registered under the IMPORT PATH src spells, the same split resolveImport
// makes. It is a parameter because the declarations live in internal/spell, whose
// own tests import this package.
func compileExample(src string, decls func(module string) (string, bool)) error {
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	defer func() { _ = sess.Close() }()

	buzzstd.Register(sess)
	// A declaration may import another, magus importing magus/agent, so the
	// imports are followed through each declaration registered.
	pending := []string{src}
	seen := map[string]bool{}
	for _, sm := range AllSource() {
		sess.SetModuleDecls(sm.ImportPath(), sm.Source)
		seen[sm.ImportPath()] = true
		pending = append(pending, sm.Source)
	}
	for len(pending) > 0 {
		text := pending[0]
		pending = pending[1:]
		for _, m := range importLine.FindAllStringSubmatch(text, -1) {
			importPath := m[1]
			if seen[importPath] {
				continue
			}
			seen[importPath] = true
			d, ok := decls(importPath)
			if !ok {
				d, ok = decls(importPath[strings.LastIndex(importPath, "/")+1:])
			}
			if ok {
				sess.SetModuleDecls(importPath, d)
				pending = append(pending, d)
			}
		}
	}

	_, err := sess.Compile(src)
	return err
}
