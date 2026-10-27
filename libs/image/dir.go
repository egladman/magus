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

// SymlinkPolicy controls symlink sources under a DirPlacement.
type SymlinkPolicy string

const (
	SymlinkError  SymlinkPolicy = "error"
	SymlinkKeep   SymlinkPolicy = "keep"
	SymlinkFollow SymlinkPolicy = "follow"
)

// ModePolicy chooses whether host permission bits are kept or overwritten.
type ModePolicy string

const (
	ModeSet      ModePolicy = "set"
	ModePreserve ModePolicy = "preserve"
)

// DirPlacement expands a host directory into layer entries under destination.
type DirPlacement struct {
	Source      HostDir
	Destination ImageDir
	// Strip is tar --strip-components on basename(source)/rel (0 keeps the dir name).
	Strip      int
	Include    []string
	Exclude    []string
	MaxDepth   int // 0 = unbounded
	ModePolicy ModePolicy
	FileMode   Mode
	DirMode    Mode
	UID, GID   int
	Symlinks   SymlinkPolicy
	EmptyDirs  bool
	MaxFiles   int
	MaxBytes   int64
}

func (d DirPlacement) defaults() DirPlacement {
	if d.ModePolicy == "" {
		d.ModePolicy = ModeSet
	}
	if d.FileMode == 0 {
		d.FileMode = ModeFile
	}
	if d.DirMode == 0 {
		d.DirMode = ModeExecutable
	}
	if d.Symlinks == "" {
		d.Symlinks = SymlinkError
	}
	return d
}

// expand turns d into files, symlinks and explicit directory destinations.
func (d DirPlacement) expand(ctx context.Context) (files []File, symlinks []Symlink, dirs []string, err error) {
	d = d.defaults()
	if d.Strip < 0 {
		return nil, nil, nil, fmt.Errorf("image.assemble: strip %d (want >= 0)", d.Strip)
	}
	if err := validateDestination(d.Destination.Value); err != nil {
		return nil, nil, nil, err
	}
	root := d.Source.Resolve()
	info, err := os.Lstat(root)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("image.assemble: dir %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, nil, nil, fmt.Errorf("image.assemble: dir %q: not a directory", root)
	}

	var fileCount int
	var byteSum int64
	err = filepath.WalkDir(root, func(p string, de fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		depth := strings.Count(rel, "/") + 1
		if d.MaxDepth > 0 && depth > d.MaxDepth {
			if de.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !matchGlobs(rel, d.Include, true) || matchGlobs(rel, d.Exclude, false) {
			if de.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		_, dest, err := d.destFor(rel)
		if err != nil {
			return err
		}
		fi, err := de.Info()
		if err != nil {
			return fmt.Errorf("image.assemble: dir entry %q: %w", p, err)
		}
		mode := fi.Mode()
		switch {
		case mode.IsRegular():
			fileCount++
			if d.MaxFiles > 0 && fileCount > d.MaxFiles {
				return fmt.Errorf("image.assemble: dir %q: exceeds maxFiles %d", root, d.MaxFiles)
			}
			byteSum += fi.Size()
			if d.MaxBytes > 0 && byteSum > d.MaxBytes {
				return fmt.Errorf("image.assemble: dir %q: exceeds maxBytes %d", root, d.MaxBytes)
			}
			fm := d.FileMode.FileMode()
			if d.ModePolicy == ModePreserve {
				fm = mode.Perm()
			}
			files = append(files, File{Source: p, Destination: dest, Mode: fm, UID: d.UID, GID: d.GID})
		case mode.IsDir():
			if d.EmptyDirs {
				dirs = append(dirs, dest)
			}
		case mode&fs.ModeSymlink != 0:
			switch d.Symlinks {
			case SymlinkError:
				return fmt.Errorf("image.assemble: dir %q: symlink source %q (symlinks=error)", root, rel)
			case SymlinkKeep:
				target, err := os.Readlink(p)
				if err != nil {
					return err
				}
				symlinks = append(symlinks, Symlink{Destination: dest, Target: target, UID: d.UID, GID: d.GID})
			case SymlinkFollow:
				return fmt.Errorf("image.assemble: dir %q: symlink follow not implemented for %q", root, rel)
			}
		default:
			return fmt.Errorf("image.assemble: dir %q: unsupported source type %s at %q", root, mode.Type(), rel)
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return files, symlinks, dirs, nil
}

// destFor maps rel under Source through Strip onto Destination; member is the pre-strip path.
func (d DirPlacement) destFor(rel string) (member, dest string, err error) {
	base := path.Base(d.Source.Resolve())
	if rel == "" || rel == "." {
		member = base
	} else {
		member = path.Clean(base + "/" + rel)
	}
	stripped, err := stripComponents(member, d.Strip)
	if err != nil {
		return member, "", err
	}
	if stripped == "" || stripped == "." {
		return member, "", fmt.Errorf("image.assemble: strip %d removes entire path %q", d.Strip, member)
	}
	return member, path.Clean(d.Destination.Value + "/" + stripped), nil
}

// stripComponents is tar --strip-components: drop the first n slash-separated parts of p.
func stripComponents(p string, n int) (string, error) {
	if n < 0 {
		return "", fmt.Errorf("image.assemble: strip %d (want >= 0)", n)
	}
	if n == 0 {
		return p, nil
	}
	parts := strings.Split(p, "/")
	if n >= len(parts) {
		return "", fmt.Errorf("image.assemble: strip %d removes entire path %q", n, p)
	}
	return strings.Join(parts[n:], "/"), nil
}

func matchGlobs(rel string, globs []string, emptyMatch bool) bool {
	if len(globs) == 0 {
		return emptyMatch
	}
	for _, g := range globs {
		ok, err := doublestar.Match(g, rel)
		if err == nil && ok {
			return true
		}
	}
	return false
}
