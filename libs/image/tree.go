package image

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// expand walks t.Source and returns one entry per file and symlink it places.
// Each entry's origin is origin plus its path relative to the source. A tree
// that places nothing is refused, as is executable-bit that marks no file.
func (t Tree) expand(ctx context.Context, origin string) ([]entry, error) {
	out, included, err := t.walk(ctx, origin)
	if err != nil {
		return nil, err
	}
	switch {
	case len(out) > 0:
		return out, t.checkExecutable(out)
	case len(t.Include) > 0 && included == 0:
		return nil, fmt.Errorf("include %q matches no file: a pattern matches each file's path relative to source, and * stops at a slash (**/ reaches nested files)", t.Include)
	case included > 0:
		return nil, fmt.Errorf("exclude %q leaves none of the %d files included", t.Exclude, included)
	default:
		return nil, fmt.Errorf("holds no file: a tree places files and symlinks, and an empty directory is a Directory")
	}
}

// checkExecutable refuses executable-bit when every file would land at
// ModeFile. The policy then does nothing, and the program meant to run ships
// without its execute bit.
func (t Tree) checkExecutable(out []entry) error {
	if t.ModePolicy != ModePolicyExecutableBit {
		return nil
	}
	var first string
	files := 0
	for _, e := range out {
		if e.kind != EntryFile {
			continue
		}
		if e.mode == ModeExecutable {
			return nil
		}
		if files == 0 {
			first = e.path
		}
		files++
	}
	if files == 0 {
		return nil
	}
	return fmt.Errorf("modePolicy=executable-bit, but none of its %d files has the host owner-execute bit, so each would ship %s (%s among them): chmod u+x the program on the host, or use modePolicy=fixed", files, ModeFile, first)
}

// walk returns the entries t places and how many files its include took
// before exclude ran.
func (t Tree) walk(ctx context.Context, origin string) ([]entry, int, error) {
	root, err := filepath.Abs(t.Source)
	if err != nil {
		return nil, 0, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, 0, err
	}
	if !info.IsDir() {
		return nil, 0, fmt.Errorf("%s is not a directory", root)
	}
	var out []entry
	included := 0
	err = filepath.WalkDir(root, func(p string, de fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if de.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		in, err := t.includes(rel)
		if err != nil || !in {
			return err
		}
		included++
		dropped, err := anyMatch(t.Exclude, rel)
		if err != nil || dropped {
			return err
		}
		e, err := t.place(root, p, rel, de)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		e.origin = origin + " " + rel
		out = append(out, e)
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return out, included, nil
}

func (t Tree) includes(rel string) (bool, error) {
	if len(t.Include) == 0 {
		return true, nil
	}
	return anyMatch(t.Include, rel)
}

// anyMatch reports whether a pattern matches rel or a directory above it, so
// "etc/ssl" takes etc/ssl/ca.pem as gitignore and tar --exclude do.
func anyMatch(patterns []string, rel string) (bool, error) {
	for _, g := range patterns {
		for p := rel; p != "."; p = path.Dir(p) {
			ok, err := doublestar.Match(g, p)
			if err != nil || ok {
				return ok, err
			}
		}
	}
	return false, nil
}

func (t Tree) place(root, p, rel string, de fs.DirEntry) (entry, error) {
	dest := path.Join(t.Path, rel)
	switch typ := de.Type(); {
	case typ.IsRegular():
		info, err := de.Info()
		if err != nil {
			return entry{}, err
		}
		return t.file(p, dest, info), nil
	case typ&fs.ModeSymlink != 0:
		return t.symlink(root, p, rel, dest)
	default:
		return entry{}, fmt.Errorf("unsupported source type %s", typ)
	}
}

func (t Tree) file(source, dest string, info fs.FileInfo) entry {
	mode := ModeFile
	if t.ModePolicy == ModePolicyExecutableBit && info.Mode().Perm()&0o100 != 0 {
		mode = ModeExecutable
	}
	return entry{path: dest, kind: EntryFile, source: source, size: info.Size(), mode: mode, uid: t.UID, gid: t.GID}
}

func (t Tree) symlink(root, p, rel, dest string) (entry, error) {
	switch t.Symlinks {
	case SymlinkPolicyKeep:
		target, err := os.Readlink(p)
		if err != nil {
			return entry{}, err
		}
		if path.IsAbs(target) {
			return entry{}, fmt.Errorf("symlink to %q: an absolute target names a host path, so use symlinks=follow or a Symlink placement", target)
		}
		if resolved := path.Join(path.Dir(rel), target); resolved == ".." || strings.HasPrefix(resolved, "../") {
			return entry{}, fmt.Errorf("symlink to %q leaves the tree", target)
		}
		return entry{path: dest, kind: EntrySymlink, target: target, uid: t.UID, gid: t.GID}, nil
	case SymlinkPolicyFollow:
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			return entry{}, err
		}
		realRoot, err := filepath.EvalSymlinks(root)
		if err != nil {
			return entry{}, err
		}
		if inside, err := filepath.Rel(realRoot, real); err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) {
			return entry{}, fmt.Errorf("symlink resolves to %s, outside the tree", real)
		}
		info, err := os.Stat(real)
		if err != nil {
			return entry{}, err
		}
		if !info.Mode().IsRegular() {
			return entry{}, fmt.Errorf("symlink resolves to %s, which is not a regular file", real)
		}
		return t.file(real, dest, info), nil
	default:
		return entry{}, fmt.Errorf("symlink source (symlinks=%s)", t.Symlinks)
	}
}
