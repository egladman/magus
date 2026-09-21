package describe

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/spells"
)

// loadSkipDirs are the directory names the walk never descends. Every one of them holds
// a foreign or generated tree, and a magusfile inside one is not a magusfile THIS
// workspace loads: it belongs to a vendored dependency, a sibling checkout parked under
// .claude/worktrees, or a package manager's cache.
var loadSkipDirs = []string{
	".claude", ".git", ".hg", ".jj", ".magus", ".sl",
	"__pycache__", "dist", "node_modules", "target", "vendor",
}

// magusYAML and the two magusfile forms, spelled once. FindAll in internal/interp owns
// the same two forms for LOADING them; this package cannot import it (interp reaches the
// engine, and the guard and the job store both read this), so the names are repeated and
// the divergence risk is stated rather than hidden.
const (
	magusYAML     = "magus.yaml"
	magusfileName = "magusfile.buzz"
	magusfilesDir = "magusfiles"
)

// WorkspaceLoadFiles names every file under root that magus must READ before any target
// in the workspace can run: each project's magusfile (either the single-file
// magusfile.buzz or the magusfiles/ directory form), the magus.yaml beside it, and every
// workspace-local spell source those magusfiles import.
//
// It is the classification behind the shared-checkout refusal, and the reason that
// refusal is not a hardcoded list. A half-saved file in this set does not break the
// worker that saved it; it breaks the WORKSPACE, so every other worker in the checkout
// loses `magus run`, `magus ls` and its own tests until the save lands. Which files those
// are is a fact about the tree, and a workspace that adds a project or a spell adds to
// this set without anyone remembering to.
//
// Paths are workspace-relative, slash-separated and sorted, and only files that EXIST are
// returned: an import that resolves to nothing cannot be half-saved.
//
// STATIC, like the rest of this package: the imports are read off the parsed source and
// no target body runs, so the answer is the same whether or not the workspace currently
// loads. That matters here more than anywhere else, since the case this serves is a
// workspace that has already stopped loading.
func WorkspaceLoadFiles(root string) []string {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	found := map[string]bool{}
	add := func(rel string) {
		if rel == "" {
			return
		}
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return
		}
		found[rel] = true
	}
	for _, dir := range loadDirs(root) {
		add(path.Join(dir, magusYAML))
		for _, src := range magusfileSources(root, dir) {
			add(src)
			for _, spell := range importedSpellSources(root, src) {
				add(spell)
			}
		}
	}
	out := make([]string, 0, len(found))
	for rel := range found {
		out = append(out, rel)
	}
	slices.Sort(out)
	return out
}

// loadDirs returns every workspace-relative directory the walk considers, "." first.
// Skipping loadSkipDirs is what keeps a vendored tree's magusfile, and a sibling
// worktree parked under .claude, out of this workspace's load set.
func loadDirs(root string) []string {
	dirs := []string{"."}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		// An unreadable directory holds no load file this walk can name, and a classifier
		// that fails on one would be useless exactly where it is needed: the case it
		// serves is a workspace already in a state nothing else can read.
		if err != nil || d == nil || !d.IsDir() {
			return nil //nolint:nilerr // see above: a directory this walk cannot read declares nothing
		}
		rel := strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(p, root)), "/")
		if rel == "" {
			return nil
		}
		if slices.Contains(loadSkipDirs, d.Name()) {
			return filepath.SkipDir
		}
		dirs = append(dirs, rel)
		return nil
	})
	return dirs
}

// magusfileSources returns dir's magusfile sources, workspace-relative. The directory
// form wins where both exist, matching the load path, which refuses the pair outright:
// this is a classifier and never the thing that reports that conflict.
func magusfileSources(root, dir string) []string {
	abs := filepath.Join(root, filepath.FromSlash(dir))
	if entries, err := filepath.Glob(filepath.Join(abs, magusfilesDir, "*.buzz")); err == nil && len(entries) > 0 {
		out := make([]string, 0, len(entries))
		for _, e := range entries {
			out = append(out, path.Join(dir, magusfilesDir, filepath.Base(e)))
		}
		slices.Sort(out)
		return out
	}
	return []string{path.Join(dir, magusfileName)}
}

// importedSpellSources resolves src's path-style imports to workspace-local spell
// sources, workspace-relative.
//
// The resolution mirrors the loader's: a bare `import "spells/house"` is searched at every
// level from the workspace root down to the importing file's own directory, in both
// accepted layouts (`<path>.buzz` and `<path>/spell.buzz`). Unlike the loader this keeps
// EVERY level that exists rather than the root-most one alone: precedence decides which
// source the workspace binds, and a shadowing source is still a file whose half-saved
// state stops the load.
func importedSpellSources(root, src string) []string {
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(src)))
	if err != nil {
		return nil
	}
	prog, perr := buzz.ParseEmbedded(string(body))
	if perr != nil || prog == nil {
		return nil // best-effort, as Extract is: an unparsable source yields nothing
	}
	var out []string
	for _, stmt := range prog.Stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok || !localSpellImport(imp.Path) {
			continue
		}
		for _, level := range spellSearchLevels(path.Dir(src)) {
			for _, rel := range []string{imp.Path + ".buzz", path.Join(imp.Path, "spell.buzz")} {
				candidate := path.Join(level, rel)
				if _, serr := os.Stat(filepath.Join(root, filepath.FromSlash(candidate))); serr == nil {
					out = append(out, candidate)
				}
			}
		}
	}
	return out
}

// localSpellImport reports whether an import path can name a workspace-local spell
// source. The three reserved prefixes are resolved by the runtime and live in no file a
// worker can save: a built-in spell is compiled into the binary, a project import names
// another magusfile, and a host module is Go. A remote spell lives in the user cache.
func localSpellImport(p string) bool {
	if p == "" || strings.HasPrefix(p, "./") || strings.HasPrefix(p, "../") || path.IsAbs(p) || spells.IsRemoteImport(p) {
		return false
	}
	for _, reserved := range []string{"magus/", "project/", "std/"} {
		if strings.HasPrefix(p, reserved) {
			return false
		}
	}
	return true
}

// spellSearchLevels returns the directory chain from the workspace root down to dir,
// root-most first, as workspace-relative paths.
func spellSearchLevels(dir string) []string {
	levels := []string{"."}
	if dir = path.Clean(dir); dir == "." || dir == "/" {
		return levels
	}
	parts := strings.Split(dir, "/")
	for i := range parts {
		levels = append(levels, path.Join(parts[:i+1]...))
	}
	return levels
}
