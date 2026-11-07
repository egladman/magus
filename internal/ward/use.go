package ward

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/types"
)

// workspaceMarkers are the files any one of which makes a directory a
// workspace a `use` entry may name.
var workspaceMarkers = []string{"magus.yaml", ".magus.yaml", "magusfile.buzz"}

// ResolveUse resolves one magus.yaml `use` entry of the workspace at root.
// path is absolute or relative to root. It refuses with MGS1054 a path that
// is missing, not a directory, holds no workspace, or is root itself.
// Nothing is read beyond a stat of each.
func ResolveUse(root, name, path string, share []types.ShareKind) (types.UsedWorkspace, *types.DiagnosticError) {
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(root, filepath.FromSlash(path))
	}
	abs = filepath.Clean(abs)
	refuse := func(why string) (types.UsedWorkspace, *types.DiagnosticError) {
		return types.UsedWorkspace{}, types.DiagnosticErrorf(types.UseWorkspaceUnavailable,
			"magus.yaml use %q: %s %s, so its rules cannot apply here. "+
				"Point path at the root of a magus workspace, or remove the entry.",
			name, abs, why)
	}
	fi, err := os.Stat(abs)
	switch {
	case err != nil:
		return refuse("does not exist")
	case !fi.IsDir():
		return refuse("is not a directory")
	}
	if !slices.ContainsFunc(workspaceMarkers, func(m string) bool {
		_, err := os.Stat(filepath.Join(abs, m))
		return err == nil
	}) {
		return refuse("holds no magus.yaml or magusfile.buzz")
	}
	if self, err := os.Stat(root); err == nil && os.SameFile(self, fi) {
		return refuse("is this workspace")
	}
	return types.UsedWorkspace{Name: name, Root: abs, Share: share}, nil
}
