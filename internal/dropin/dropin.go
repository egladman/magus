// Package dropin reads a configuration directory of one file per entry.
//
// The shape is the one /etc/yum.repos.d and /etc/apt/sources.list.d settled on,
// and it is worth being precise about what earns it, because it is not free: a
// directory has to be enumerated, ordering has to be defined, and it is a second
// place to look. A drop-in pays for that when
//
//   - each entry is meaningful on its own, so a reader can understand one file
//     without the others;
//   - entries are added and removed one at a time, so adding is a copy and removing
//     is an `rm` - both of which work when the tool that owns them does not; and
//   - separate writers would otherwise contend on one file, which is a lock, a
//     retry, and a stale-lock heuristic that all disappear here.
//
// It does NOT pay for a log or a time series - no entry means anything alone,
// nobody hand-authors one, and one file per record is thousands of files. magus's
// run history stays a single file for exactly that reason.
package dropin

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Entry is one file found in a drop-in directory.
type Entry struct {
	// Name is the filename with its extension removed - the identity a human uses
	// to talk about the entry, and the thing `rm <name>.<ext>` removes.
	Name string
	Path string
	Data []byte
}

// Read returns every *.<ext> file in dir, sorted by name so callers see a stable
// order regardless of what the filesystem hands back.
//
// A missing directory yields no entries and no error: nothing configured is a
// normal state, not a failure. A file that cannot be READ is an error, because
// silently skipping it would present a smaller configuration as if it were the
// whole one - the failure mode a drop-in directory is most prone to.
//
// ext is given without a dot ("json", "yaml"). Anything else in the directory is
// ignored, which is what lets a writer stage a complete temp file under another
// name and publish it atomically.
func Read(dir, ext string) ([]Entry, error) {
	suffix := "." + ext
	dirEntries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}

	var out []Entry
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), suffix) {
			continue
		}
		path := filepath.Join(dir, de.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		out = append(out, Entry{
			Name: strings.TrimSuffix(de.Name(), suffix),
			Path: path,
			Data: data,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ValidName reports whether name is safe to use as a drop-in filename.
//
// The check exists because in a drop-in directory the entry's NAME is a path
// component. A name carrying a separator is a traversal and one starting with a
// dot is a file the directory listing hides, neither of which is a cosmetic
// problem the way it would be inside a single file's array.
func ValidName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("name is required")
	case len(name) > 64:
		return fmt.Errorf("name %q is longer than 64 characters", name)
	case strings.HasPrefix(name, "."):
		return fmt.Errorf("name %q may not start with a dot", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.':
		default:
			return fmt.Errorf("name %q may only hold letters, digits, dot, dash, and underscore", name)
		}
	}
	return nil
}

// Publish writes data to dir/<name>.<ext> so that no reader ever observes a
// partial file, and reports fs.ErrExist when the name is already taken.
//
// A complete temp file is written first and then hard-linked into place. Both
// halves matter: the temp name does not carry ext, so Read never globs it, and
// the link is atomic and fails when the destination exists - which makes it the
// uniqueness check as well as the publication.
//
// Creating the final path with O_EXCL and writing into it afterwards is the
// obvious alternative and is wrong: it publishes an empty file first, and any
// reader in that window sees a truncated entry.
func Publish(dir, name, ext string, data []byte, perm os.FileMode) error {
	if err := ValidName(name); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create entry in %s: %w", dir, err)
	}
	defer os.Remove(tmp.Name()) // the link publishes; the temp is always litter
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}

	path := filepath.Join(dir, name+"."+ext)
	if err := os.Link(tmp.Name(), path); err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%q: %w", name, fs.ErrExist)
		}
		return fmt.Errorf("publish %s: %w", path, err)
	}
	return nil
}
