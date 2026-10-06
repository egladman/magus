package scipbuzz

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// module is what an import path names: a workspace file, or, when no workspace
// file answers to it, an external module known only by its path.
type module struct {
	path string // the import path less any `buzz:` prefix
	file *file  // nil for an external module
}

// bindingName is the name an import without an alias binds: the last segment of
// its path.
func bindingName(importPath string) string {
	return path.Base(strings.TrimPrefix(importPath, "buzz:"))
}

// resolve maps an import in from to a module. Like gopherbuzz's findIncludeFile it
// tries the importing file's directory first; where gopherbuzz then walks the
// directories of the files that imported this one, a static reader has no import
// chain, so it tries the project root and then the workspace root, which is where
// the chains in a magus workspace start. Only a file inside the workspace counts:
// its path is what names its symbols.
func (ix *indexer) resolve(from *file, importPath string) *module {
	trimmed := strings.TrimPrefix(importPath, "buzz:")
	m := &module{path: trimmed}
	if trimmed == "" || filepath.IsAbs(trimmed) {
		return m
	}
	name := trimmed
	if filepath.Ext(name) != ".buzz" {
		name += ".buzz"
	}
	for _, dir := range []string{filepath.Dir(from.abs), ix.project, ix.workspace} {
		candidate := filepath.Join(dir, filepath.FromSlash(name))
		if _, ok := ix.workspaceRel(candidate); !ok {
			continue
		}
		if st, err := os.Stat(candidate); err != nil || !st.Mode().IsRegular() {
			continue
		}
		if f := ix.load(candidate); f != nil {
			m.file = f
			return m
		}
	}
	return m
}

// workspaceRel returns abs as a slash path relative to the workspace root, and
// false when abs lies outside it.
func (ix *indexer) workspaceRel(abs string) (string, bool) {
	rel, err := filepath.Rel(ix.workspace, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
