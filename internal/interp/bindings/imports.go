package bindings

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/ast"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/types"
)

// resolveProjectImport resolves `import "project/<path>"` to a module of the named
// project's targets, each a cross-project handle ({mode literal, pattern <target>,
// project <path>}) that magus.needs dispatches across the boundary. The path is
// dot-relative to the importing magusfile's directory (matching how the graph and
// runtime resolve cross deps). Target names are read statically by scanning the
// dependency's `export fun` declarations — no VM load, so no import-time recursion.
// Returns ok=false for any non-"project/" import.
func resolveProjectImport(ctx context.Context, importPath string) (vm.Value, bool) {
	const prefix = "project/"
	if !strings.HasPrefix(importPath, prefix) {
		return vm.Null, false
	}
	raw := strings.TrimPrefix(importPath, prefix)
	src := interp.SourceFromContext(ctx)
	if src == nil || raw == "" {
		return vm.Null, false
	}
	depDir := filepath.Clean(filepath.Join(src.Dir, filepath.FromSlash(raw)))
	srcs, err := interp.FindAll(depDir)
	if err != nil {
		return vm.Null, false
	}
	norm := types.DefaultTargetNameNormalizer.NormalizeTargetName
	m := vm.NewMap()
	for _, s := range srcs {
		if s.Engine != "buzz" {
			continue
		}
		for _, f := range s.Files {
			b, rerr := os.ReadFile(f)
			if rerr != nil {
				continue
			}
			prog, perr := buzz.ParseEmbedded(string(b))
			if perr != nil || prog == nil {
				continue
			}
			// Member key is the raw `export fun` identifier (so <alias>.build_playground
			// resolves); the handle's target is the kebab-normalized run name.
			for _, stmt := range prog.Stmts {
				fn, ok := stmt.(*ast.FunDecl)
				if !ok || !fn.IsExported {
					continue
				}
				m.MapSet(fn.Name, targetQueryToBuzz(types.TargetQuery{
					Mode:    types.QueryLiteral,
					Pattern: norm(fn.Name),
					Project: raw,
				}))
			}
		}
	}
	return m, true
}

// resolveLocalSpellImport resolves a path-style import (e.g. "spells/hello") to a
// workspace-local spell at <importPath>.buzz, relative to the process cwd (the
// magusfile's directory at run time). It returns the
// spell handle and ok=true when a file exists and parses as a spell; otherwise
// ok=false, leaving the import to the normal file search.
func resolveLocalSpellImport(ctx context.Context, importPath string) (vm.Value, bool) {
	// Resolve relative to the magusfile's own directory first, so a magusfile
	// imported from outside its dir (e.g. workspace preload visiting a sub-project)
	// still finds its ./spells; fall back to cwd for the run-from-here case.
	dirs := []string{}
	if src := interp.SourceFromContext(ctx); src != nil && src.Dir != "" {
		dirs = append(dirs, src.Dir)
	}
	dirs = append(dirs, "")
	for _, dir := range dirs {
		// Two layouts are accepted: a flat spells/<name>.buzz, and the directory
		// convention spells/<name>/spell.buzz (preferred — keeps a spell's source
		// and any future companion files together, easy to discover).
		for _, rel := range []string{importPath + ".buzz", filepath.Join(importPath, "spell.buzz")} {
			path := rel
			if dir != "" {
				path = filepath.Join(dir, rel)
			}
			if fi, err := os.Stat(path); err != nil || fi.IsDir() {
				continue
			}
			// loadLocalSpell absolutizes a relative path and registers the Buzz spell
			// with handler op support, so the returned handle's name resolves to a
			// handler op-capable spell whether it is bound to a project or wired as
			// the remote cache backend.
			if m, ok := loadLocalSpell(ctx, path); ok {
				return spellHandleFromMeta(m), true
			}
		}
	}
	return vm.Null, false
}
