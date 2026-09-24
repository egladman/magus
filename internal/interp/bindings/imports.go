package bindings

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/parsecache"
	"github.com/egladman/magus/internal/spell"
	remotespell "github.com/egladman/magus/internal/spell/remote"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// resolveProjectImport resolves `import "project/<path>"` to a module of the named
// project's targets, each a callable cross-project handle: a function value that
// dispatches the target across the boundary when invoked, and that ctx.needs
// recognizes by identity (via ext) to declare the dependency. The path is
// dot-relative to the importing magusfile's directory (matching how the graph and
// runtime resolve cross deps). Target names are read statically by scanning the
// dependency's `export fun` declarations — no VM load, so no import-time recursion.
// Returns ok=false for any non-"project/" import.
func resolveProjectImport(ctx context.Context, importPath string, ext *externalHandles) (vm.Value, bool) {
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
	norm := types.Normalize
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
			prog, perr := parsecache.Shared().ParseEmbedded(string(b))
			if perr != nil || prog == nil {
				continue
			}
			// Member key is the raw `export fun` identifier (so <alias>.build_playground
			// resolves); the handle's target is the kebab-normalized run name. Each
			// member is a real function value: invoking it dispatches the cross-project
			// target, and ctx.needs recognizes it by identity through ext.
			for _, stmt := range prog.Stmts {
				fn, ok := stmt.(*ast.FunDecl)
				if !ok || !fn.IsExported {
					continue
				}
				ref := externalTarget{Project: raw, Target: norm(fn.Name)}
				handle := vm.DirectValue(raw+"."+fn.Name, func(callCtx context.Context, _ []vm.Value) (vm.Value, error) {
					return vm.Null, dispatchBuzzExternal(callCtx, ref)
				})
				ext.register(handle, ref)
				m.MapSet(fn.Name, handle)
			}
		}
	}
	// The reserved `.file(rel)` member: a general cross-project path resolver, not a
	// target. It returns the authoritative WORKSPACE-relative path of a file in the
	// imported project, resolved with the SAME file.ResolveImport formula describe.go
	// applies to the extracted ref, so a ctx.readsFiles(<alias>.file(...)) declaration agrees
	// between static analysis and runtime. (The extractor itself stores the path as
	// written and never resolves; resolution happens in describe.go.) The returned path is a usable value: pass it to ctx.readsFiles
	// to declare a cross-project input, or use it directly (e.g. an exec argv). It
	// registers nothing itself.
	// Set after the target loop so it reserves the name even if a target is called file.
	m.MapSet(types.CrossFileMember, vm.DirectValue(raw+"."+types.CrossFileMember, func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		var rel string
		if len(args) > 0 && args[0].IsStr() {
			rel = args[0].AsString()
		}
		joined := path.Join(raw, filepath.ToSlash(rel))
		// No workspace in context (a bare `magus buzz` script): degrade to a cleaned
		// relative path rather than error. The real run path always carries the workspace.
		ws := types.WorkspaceFromContext(callCtx)
		if ws == nil {
			return vm.StrValue(path.Clean(joined)), nil
		}
		callerRel, err := filepath.Rel(ws.Root(), src.Dir)
		if err != nil {
			return vm.StrValue(path.Clean(joined)), nil //nolint:nilerr // best-effort: degrade to the cleaned path rather than aborting the target
		}
		resolved, err := file.ResolveImport(joined, filepath.ToSlash(callerRel))
		if err != nil {
			// Match the static extractor's best-effort drop and the branches above:
			// degrade to the cleaned path rather than aborting the target at runtime.
			return vm.StrValue(path.Clean(joined)), nil //nolint:nilerr // deliberate best-effort degradation, see comment above
		}
		return vm.StrValue(resolved), nil
	}))
	return m, true
}

// resolveLocalSpellImport resolves a path-style import (e.g. "spells/hello") to a
// workspace-local spell, returning the spell handle and ok=true when a file exists
// and parses as a spell; otherwise ok=false, leaving the import to the normal file
// search. It never takes a "./"-relative path: a bare `import "spells/hello"` stays
// faithful to upstream Buzz's plain-import form.
//
// Resolution walks a spells dir at every level from the workspace root down to the
// importing file's directory, root-first (see spellSearchLevels). This accrues
// spells along a nested project's path: a spell at the workspace root is shared by
// every project, one at an intermediate dir is shared by that subtree, and one next
// to a project's magusfile is private to it. Precedence is ROOT-WINS: the first
// (root-most) match is canonical, so a shared name means one spell workspace-wide.
// A deeper level defining a name an ancestor already owns is a shadow footgun,
// guarded separately by the shadow ward at preload; this resolver just picks the
// canonical one deterministically and never errors.
func resolveLocalSpellImport(ctx context.Context, im *remotespell.Imports, importPath string) (vm.Value, bool) {
	for _, dir := range spellSearchLevels(ctx) {
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
				if err := embeddedShadow(im, m.Name, filepath.Dir(path)); err != nil {
					return importFailed(ctx, err)
				}
				return spellHandleFromMeta(m), true
			}
		}
	}
	return vm.Null, false
}

// resolveRemoteSpellImport binds a spell imported by registry path from its verified
// copy under the workspace's view, where <view>/<import path>/spell.buzz is its entry.
// It always claims the import, binding null on failure, because returning false would
// hand the path to the file search, where a crafted local directory could answer it.
func resolveRemoteSpellImport(ctx context.Context, im *remotespell.Imports, importPath string) (vm.Value, bool) {
	dir, err := im.Dir(importPath)
	if err != nil {
		return importFailed(ctx, err)
	}
	m, ok := loadLocalSpell(ctx, filepath.Join(dir, "spell.buzz"))
	if !ok {
		return importFailed(ctx, fmt.Errorf("remote spell %s: its spell.buzz does not load as a spell", importPath))
	}
	if err := embeddedShadow(im, m.Name, dir); err != nil {
		return importFailed(ctx, err)
	}
	return spellHandleFromMeta(m), true
}

// resolveOverrideImport binds the workspace copy magus.yaml declares for importPath. An
// embedded spell's copy must carry that spell's name, and takes over its registry entry,
// so every use of the name runs the copy; a remote spell's copy registers like any
// workspace spell.
func resolveOverrideImport(ctx context.Context, im *remotespell.Imports, importPath, dir string) (vm.Value, bool) {
	spec, sp, err := newBuzzSpell(ctx, filepath.Join(dir, "spell.buzz"))
	if err != nil {
		return importFailed(ctx, types.WrapDiagnostic(types.SpellOverrideInvalid, err,
			"magus.yaml spells.%s: %s does not load as a spell: %v", importPath, dir, err))
	}
	name, embedded := strings.CutPrefix(importPath, spells.ModulePrefix)
	switch {
	case embedded && spec.Name != name:
		return importFailed(ctx, types.DiagnosticErrorf(types.SpellOverrideInvalid,
			"magus.yaml spells.%s: %s holds the spell %q, not %q, so it cannot replace it", importPath, dir, spec.Name, name))
	case embedded:
		project.DefaultSpellRegistry().ReplaceSpell(sp)
	default:
		if err := embeddedShadow(im, spec.Name, dir); err != nil {
			return importFailed(ctx, err)
		}
		project.DefaultSpellRegistry().RegisterIfAbsent(sp)
	}
	return spellHandleFromMeta(spec), true
}

// embeddedShadow refuses a workspace or remote spell carrying an embedded spell's name
// unless magus.yaml declares it as that spell's replacement. Registration is by name,
// so an undeclared one would silently bind the embedded spell under a handle that
// reads like the workspace copy.
func embeddedShadow(im *remotespell.Imports, name, dir string) error {
	if _, builtin := spell.Builtins()[name]; !builtin {
		return nil
	}
	module := spells.ModulePath(name)
	if declared, ok := im.Override(module); ok && filepath.Clean(declared) == filepath.Clean(dir) {
		return nil
	}
	return types.DiagnosticErrorf(types.SpellShadowed,
		"spell %q at %s has the name of the embedded spell %s; declare `spells: {%s: {path: <dir>}}` in magus.yaml to replace the embedded one, or rename it",
		name, dir, module, module)
}

// importFailed claims an import the resolver could not bind, reporting err to the load
// in progress so it fails with the cause; outside a load, it is logged.
func importFailed(ctx context.Context, err error) (vm.Value, bool) {
	if !interp.ReportImportError(ctx, err) {
		slog.ErrorContext(ctx, "spell import", "err", err)
	}
	return vm.Null, true
}

// spellSearchLevels returns the directories a path-style spell import is searched
// against, in resolution order: the workspace root first, then each nested level
// down to the importing file's own directory (root-wins), then "" (the process
// cwd) as an out-of-workspace fallback for a `magus buzz` script with no workspace.
// The walk is bounded at the workspace root so resolution stays hermetic and never
// reaches for spells outside the workspace.
func spellSearchLevels(ctx context.Context) []string {
	var start, root string
	if src := interp.SourceFromContext(ctx); src != nil {
		start = src.Dir
	}
	if ws := types.WorkspaceFromContext(ctx); ws != nil {
		root = ws.Root()
	}
	levels := rootFirstLevels(root, start)
	if root == "" {
		levels = append(levels, "")
	}
	return levels
}

// rootFirstLevels returns the directory chain from root down to start inclusive,
// root-most first, so a root-wins search visits the shared level before nested
// ones. It is bounded to root and its descendants: an empty root yields just start
// (or nothing), and a start outside root yields only start, so the walk never
// escapes the workspace.
func rootFirstLevels(root, start string) []string {
	absStart := absOrEmpty(start)
	absRoot := absOrEmpty(root)
	if absStart == "" {
		if absRoot != "" {
			return []string{absRoot}
		}
		return nil
	}
	within := absRoot != "" && (absStart == absRoot || strings.HasPrefix(absStart, absRoot+string(filepath.Separator)))
	if !within {
		return []string{absStart} // importing file is outside the workspace root
	}
	var up []string
	for cur := absStart; ; cur = filepath.Dir(cur) {
		up = append(up, cur)
		if cur == absRoot {
			break
		}
	}
	slices.Reverse(up)
	return up
}

// absOrEmpty returns filepath.Abs(p), or p unchanged when it cannot be resolved,
// or "" for an empty input.
func absOrEmpty(p string) string {
	if p == "" {
		return ""
	}
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}
