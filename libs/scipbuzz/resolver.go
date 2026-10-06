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

// searchTemplates are the layouts magus gives a magusfile's gopherbuzz session
// (internal/interp magusSearchPaths), tried at each root with `?` replaced by the
// import path: upstream Buzz's project-relative layouts, then magusfiles/.
var searchTemplates = []string{"?.buzz", "?/main.buzz", "?/src/main.buzz", "?/src/?.buzz", "magusfiles/?.buzz"}

// resolve maps an import in from to a module the way gopherbuzz's findIncludeFile
// does under magus. The directory of the importing file comes first. gopherbuzz
// then walks the directories of the files that imported this one, a chain a static
// reader does not have; in a magus workspace those chains start at a magusfile,
// which the project root's templates cover. Then come the templates at the
// project root and at the workspace root. Only a file inside the workspace
// counts: its path is what names its symbols.
//
// It does not see a module the host registers natively, which gopherbuzz resolves
// before any file: a workspace file named like one (std.buzz beside the importer)
// is taken for it.
func (ix *indexer) resolve(from *file, importPath string) *module {
	trimmed := strings.TrimPrefix(importPath, "buzz:")
	m := &module{path: trimmed}
	if trimmed == "" {
		return m
	}
	candidates := []string{filepath.Join(filepath.Dir(from.abs), filepath.FromSlash(trimmed)+".buzz")}
	for _, root := range []string{ix.project, ix.workspace} {
		for _, tmpl := range searchTemplates {
			candidates = append(candidates, filepath.Join(root, filepath.FromSlash(strings.ReplaceAll(tmpl, "?", trimmed))))
		}
	}
	for _, candidate := range candidates {
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
