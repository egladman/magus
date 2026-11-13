package spell

import (
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

// This file holds the generated Buzz DECLARATIONS of every host module: the object
// mirrors its methods return, then an `export extern fun` per method carrying the
// parameter and return types.
//
// It is the half that makes a host call CHECKED. A native module alone is untyped to
// the checker, so `magus\impact(base)` produced an Unknown and a magusfile
// could read a field the return does not carry, failing only at run time. An extern
// declaration is exactly the fix the language provides for this: it states the
// signature and emits no code, so it types the call without shadowing the host
// binding underneath it.
//
// Generated per module rather than hand-listed (the map that preceded this carried
// only object mirrors, for six of the twenty-three modules) so a new method or a
// changed signature cannot drift from what the checker enforces.

//go:generate go run ../../cmd/magus-utils moduledecls -outdir gen/decls
//go:embed gen/decls gen/types
var hostDeclsFS embed.FS

// GeneratedBuzz is the generated Buzz this binary was built with: the host
// declarations under gen/decls/ and the boundary types under gen/types/.
func GeneratedBuzz() fs.FS { return hostDeclsFS }

// ModuleDecls returns the Buzz declarations for a host module by import path, and
// whether any exist. A module with no generated declarations is not an error: the
// caller registers the native implementation without them, which is the
// behavior every module had before these existed.
//
// optimization: memoize each module's source for the process.
//
//	measured: BenchmarkModuleDecls, one session's registrations, -97% sec/op,
//	  139 KiB and 148 allocs/op to 0 (benchstat -col /memo, n=10).
//	trade-off: the declarations stay resident once read, under 180 KiB in all.
func ModuleDecls(module string) (string, bool) {
	if v, ok := moduleDecls.Load(module); ok {
		d, isDecl := v.(moduleDecl)
		if !isDecl {
			panic("spell: module declaration cache holds a value that is not a moduleDecl")
		}
		return d.src, d.ok
	}
	var d moduleDecl
	if b, err := fs.ReadFile(hostDeclsFS, fmt.Sprintf("gen/decls/%s.buzz", module)); err == nil {
		d = moduleDecl{src: string(b), ok: true}
	}
	moduleDecls.Store(module, d)
	return d.src, d.ok
}

type moduleDecl struct {
	src string
	ok  bool
}

var moduleDecls sync.Map

// NamespaceModules lists, sorted, the import path of every nested namespace
// with generated declarations, such as magus/agent. Its declarations are
// ModuleDecls of that path, and a module's declarations import it by path.
var NamespaceModules = sync.OnceValues(func() ([]string, error) {
	var out []string
	err := fs.WalkDir(hostDeclsFS, "gen/decls", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, ok := strings.CutPrefix(path, "gen/decls/")
		if !ok || d.IsDir() || !strings.Contains(rel, "/") {
			return nil
		}
		if module, isBuzz := strings.CutSuffix(rel, ".buzz"); isBuzz {
			out = append(out, module)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("spell: walk the embedded declarations: %w", err)
	}
	slices.Sort(out)
	return out, nil
})

// SetNamespaceDecls registers the declarations of every NamespaceModules
// path into sess, so `import "magus/agent";` resolves. A path sess already
// holds is left as it is.
func SetNamespaceDecls(sess *buzz.Session) error {
	paths, err := NamespaceModules()
	if err != nil {
		return err
	}
	for _, path := range paths {
		if sess.HasModuleDecls(path) {
			continue
		}
		src, ok := ModuleDecls(path)
		if !ok {
			return fmt.Errorf("spell: namespace %s has no declarations", path)
		}
		sess.SetModuleDecls(path, src)
	}
	return nil
}
