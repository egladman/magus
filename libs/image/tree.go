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
func (t Tree) expand(ctx context.Context) ([]entry, error) {
	root, err := filepath.Abs(t.Source)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", root)
	}
	var out []entry
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
		included, err := t.includes(rel)
		if err != nil || !included {
			return err
		}
		e, err := t.place(root, p, rel, de)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		out = append(out, e)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (t Tree) includes(rel string) (bool, error) {
	in := len(t.Include) == 0
	for _, g := range t.Include {
		ok, err := doublestar.Match(g, rel)
		if err != nil {
			return false, err
		}
		in = in || ok
	}
	if !in {
		return false, nil
	}
	for _, g := range t.Exclude {
		ok, err := doublestar.Match(g, rel)
		if err != nil || ok {
			return false, err
		}
	}
	return true, nil
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
	if t.Modes == ModeExecutableBit && info.Mode().Perm()&0o100 != 0 {
		mode = ModeExecutable
	}
	return entry{path: dest, kind: kindFile, source: source, mode: mode, uid: t.UID, gid: t.GID}
}

func (t Tree) symlink(root, p, rel, dest string) (entry, error) {
	switch t.Symlinks {
	case SymlinkKeep:
		target, err := os.Readlink(p)
		if err != nil {
			return entry{}, err
		}
		if path.IsAbs(target) {
			return entry{}, fmt.Errorf("symlink to %q: an absolute target names a host path; use symlinks=follow or a Symlink placement", target)
		}
		if resolved := path.Join(path.Dir(rel), target); resolved == ".." || strings.HasPrefix(resolved, "../") {
			return entry{}, fmt.Errorf("symlink to %q leaves the tree", target)
		}
		return entry{path: dest, kind: kindSymlink, target: target, uid: t.UID, gid: t.GID}, nil
	case SymlinkFollow:
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
