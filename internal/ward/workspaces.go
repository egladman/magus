package ward

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// ListedEntry is one knowledge.workspaces entry: its name, its root with ~
// expanded, and where it is written, for a refusal to name.
type ListedEntry struct {
	Name, Root, Where string
}

// workspaceMarkers are the files any one of which makes a directory a
// workspace an entry may name.
var workspaceMarkers = []string{"magus.yaml", ".magus.yaml", "magusfile.buzz"}

// ReconcileWorkspaces points root and every other listed workspace at each
// other under types.WorkspacesDir, in both trees, and removes every symlink no
// entry names with its counterpart. An unlisted root only removes. A missing
// entry refuses (MGS1054), as does a non-symlink in the way (MGS1058). It
// returns root's entry first, then the others, and one line per change.
func ReconcileWorkspaces(root string, entries []ListedEntry) ([]types.ListedWorkspace, []string, error) {
	self, listed := selfEntry(root, entries)
	r := reconciler{root: root, real: realpath(root)}
	if !listed {
		return nil, r.lines, r.drop(nil)
	}
	for _, e := range entries {
		if err := checkWorkspace(e); err != nil {
			return nil, nil, err
		}
	}
	want := map[string]string{}
	out := []types.ListedWorkspace{{Name: self.Name, Root: self.Root}}
	for _, e := range entries {
		if e.Name == self.Name {
			continue
		}
		want[e.Name] = e.Root
		out = append(out, types.ListedWorkspace{Name: e.Name, Root: e.Root, Dir: path.Join(types.WorkspacesDir, e.Name)})
	}
	if err := r.drop(want); err != nil {
		return nil, r.lines, err
	}
	for _, w := range out[1:] {
		if err := r.make(root, w.Name, w.Root, w.Name); err != nil {
			return nil, r.lines, err
		}
		if err := r.prune(w.Root, w.Name, self.Name); err != nil {
			return nil, r.lines, err
		}
		if err := r.make(w.Root, self.Name, self.Root, w.Name); err != nil {
			return nil, r.lines, err
		}
	}
	return out, r.lines, nil
}

// Listed reports whether an entry's root is root, comparing resolved paths.
func Listed(root string, entries []ListedEntry) bool {
	_, ok := selfEntry(root, entries)
	return ok
}

// selfEntry finds the entry naming root, comparing resolved paths.
func selfEntry(root string, entries []ListedEntry) (ListedEntry, bool) {
	here := realpath(root)
	for _, e := range entries {
		if realpath(e.Root) == here {
			return e, true
		}
	}
	return ListedEntry{}, false
}

// checkWorkspace refuses an entry whose root is gone or holds no workspace.
func checkWorkspace(e ListedEntry) error {
	why := ""
	fi, err := os.Stat(e.Root)
	switch {
	case err != nil:
		why = "which does not exist"
	case !fi.IsDir():
		why = "which is not a directory"
	case !slices.ContainsFunc(workspaceMarkers, func(m string) bool {
		_, err := os.Stat(filepath.Join(e.Root, m))
		return err == nil
	}):
		why = "which holds no magus.yaml or magusfile.buzz"
	default:
		return nil
	}
	return types.DiagnosticErrorf(types.WorkspaceMissing,
		"knowledge.workspaces entry %s points at %s, %s; delete the entry at %s, or restore the checkout",
		e.Name, e.Root, why, e.Where)
}

type reconciler struct {
	root, real string
	lines      []string
}

// drop removes every symlink under root's types.WorkspacesDir that want does
// not name, then each one's counterpart pointing back at root.
func (r *reconciler) drop(want map[string]string) error {
	dir := filepath.Join(r.root, filepath.FromSlash(types.WorkspacesDir))
	held, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("knowledge.workspaces: read %s: %w", dir, err)
	}
	for _, d := range held {
		if _, keep := want[d.Name()]; keep || d.Type()&fs.ModeSymlink == 0 {
			continue
		}
		at := filepath.Join(dir, d.Name())
		target, _ := os.Readlink(at)
		if err := os.Remove(at); err != nil {
			return fmt.Errorf("knowledge.workspaces: remove %s: %w", at, err)
		}
		r.say(d.Name(), "removed %s", r.show(at))
		if target != "" {
			if err := r.prune(target, d.Name(), ""); err != nil {
				return err
			}
		}
	}
	removeIfEmpty(dir)
	return nil
}

// prune removes each symlink under tree's types.WorkspacesDir that points at
// this root and is not named keep. label names the tree in the lines.
func (r *reconciler) prune(tree, label, keep string) error {
	dir := filepath.Join(tree, filepath.FromSlash(types.WorkspacesDir))
	held, err := os.ReadDir(dir)
	if err != nil {
		return nil //nolint:nilerr // a tree without the dir holds nothing to remove
	}
	for _, d := range held {
		at := filepath.Join(dir, d.Name())
		if d.Name() == keep || d.Type()&fs.ModeSymlink == 0 || !r.pointsHere(at) {
			continue
		}
		if err := os.Remove(at); err != nil {
			return fmt.Errorf("knowledge.workspaces: remove %s: %w", at, err)
		}
		r.say(label, "removed %s", r.show(at))
	}
	removeIfEmpty(dir)
	return nil
}

// pointsHere reports whether the symlink at names this root, as written or
// as resolved.
func (r *reconciler) pointsHere(at string) bool {
	target, err := os.Readlink(at)
	if err != nil {
		return false
	}
	return filepath.Clean(target) == filepath.Clean(r.root) || realpath(target) == r.real
}

// make points tree's types.WorkspacesDir/name at target. It retargets a
// symlink pointing elsewhere and refuses anything else there.
func (r *reconciler) make(tree, name, target, label string) error {
	dir := filepath.Join(tree, filepath.FromSlash(types.WorkspacesDir))
	at := filepath.Join(dir, name)
	fi, err := os.Lstat(at)
	switch {
	case err == nil && fi.Mode()&fs.ModeSymlink == 0:
		return types.DiagnosticErrorf(types.WorkspacesNotALink,
			"%s is not a symlink, so the load cannot point it at %s; magus deletes nothing it did not make, so move it away",
			at, target)
	case err == nil:
		got, _ := os.Readlink(at)
		if got == target {
			return nil
		}
		if err := os.Remove(at); err != nil {
			return fmt.Errorf("knowledge.workspaces: retarget %s: %w", at, err)
		}
		// The tree the entry moved from still points back here.
		if tree == r.root && got != "" {
			if err := r.prune(got, label, ""); err != nil {
				return err
			}
		}
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("knowledge.workspaces: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("knowledge.workspaces: %w", err)
	}
	if err := os.Symlink(target, at); err != nil {
		return fmt.Errorf("knowledge.workspaces: point %s at %s: %w", at, target, err)
	}
	r.say(label, "linked %s -> %s", r.show(at), r.show(target))
	return nil
}

func (r *reconciler) say(name, format string, args ...any) {
	r.lines = append(r.lines, fmt.Sprintf("workspace %s: ", name)+fmt.Sprintf(format, args...))
}

// show is p relative to this root when inside it, else absolute with the
// home directory spelled ~.
func (r *reconciler) show(p string) string {
	if rel, err := filepath.Rel(r.root, p); err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.ToSlash(rel)
	}
	if home, err := os.UserHomeDir(); err == nil {
		if rest, ok := strings.CutPrefix(p, home+string(filepath.Separator)); ok {
			return "~/" + filepath.ToSlash(rest)
		}
	}
	return p
}

// removeIfEmpty leaves no empty types.WorkspacesDir behind; os.Remove refuses
// a directory that still holds anything.
func removeIfEmpty(dir string) { _ = os.Remove(dir) }

func realpath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}
