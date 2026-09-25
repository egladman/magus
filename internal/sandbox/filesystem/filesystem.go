// Package filesystem holds the filesystem half of a sandbox policy: the path
// allowlist (Ruleset) and path-shape checks consulted before touching the filesystem.
package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ErrDenied is returned by Ruleset.Check when a requested operation falls outside
// the configured allowlist.
var ErrDenied = errors.New("sandbox: operation denied by the sandbox policy")

// Access is one kind of filesystem access a check asks about.
type Access int

const (
	Read  Access = iota // open for reading, list a directory
	Write               // write, create, remove, rename
	Exec                // execve a binary
)

// String names a for metrics and messages: read, write or exec.
func (a Access) String() string {
	switch a {
	case Read:
		return "read"
	case Write:
		return "write"
	case Exec:
		return "exec"
	}
	return fmt.Sprintf("access(%d)", int(a))
}

// Rule is one entry in the policy's filesystem allowlist. Each flag grants its
// access alone: Exec without Read permits execve and nothing else, and Read
// without Exec is enough for dlopen and mmap but not for execve. The kernel layer
// and Check read the flags the same way, so a host without landlock refuses the
// same exec a host with it does.
type Rule struct {
	Path  string
	Read  bool
	Write bool
	Exec  bool
}

// Grants reports whether r grants access.
func (r Rule) Grants(access Access) bool {
	switch access {
	case Read:
		return r.Read
	case Write:
		return r.Write
	case Exec:
		return r.Exec
	}
	return false
}

// Ruleset is the filesystem allowlist a Policy consults.
type Ruleset struct {
	Rules []Rule
}

// Check reports whether the ruleset permits access to path. path is resolved
// through every symlink first, a dangling one included, so the check lands on the
// file the operation would touch. An error wraps ErrDenied.
func (rs Ruleset) Check(path string, access Access) error {
	abs, err := normalizePath(path)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrDenied, path, err)
	}
	for _, r := range rs.Rules {
		if Under(abs, r.Path) && r.Grants(access) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s %s is outside the allowlist", ErrDenied, access, abs)
}

// maxSymlinks bounds link expansion, matching Linux's MAXSYMLINKS.
const maxSymlinks = 40

// normalizePath returns path absolute, clean, and resolved through every symlink
// in it, the way the kernel would walk it.
//
// Components are resolved in order and ".." is applied to the RESOLVED prefix, so
// /ws/l/../x with l -> /home/u/.config is /home/u/x, not /ws/x. A lexical clean
// before resolution would check a path the kernel never opens.
//
// A missing component and everything after it are kept lexically: a write target
// need not exist yet. A symlink is followed even when its target is missing, so a
// write through a dangling link is checked against where the file would be created
// rather than against the link. On Linux 5.13 and newer the landlock layer closes
// the window between this check and the operation; elsewhere it stays open.
func normalizePath(path string) (string, error) {
	if path == "" {
		return "", errors.New("sandbox: empty path")
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", errors.New("sandbox: path contains NUL")
	}
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("sandbox: %w", err)
		}
		// Joined by hand: filepath.Join cleans, and cleaning before resolution is the bug.
		path = wd + string(filepath.Separator) + path
	}
	vol := filepath.VolumeName(path)
	resolved := vol + string(filepath.Separator)
	pending := splitPath(path[len(vol):])
	links := 0
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		next := filepath.Join(resolved, c)
		info, err := os.Lstat(next)
		switch {
		case errors.Is(err, fs.ErrNotExist), errors.Is(err, syscall.ENOTDIR):
			resolved = next
			continue
		case err != nil:
			return "", fmt.Errorf("sandbox: %w", err)
		case info.Mode()&fs.ModeSymlink == 0:
			resolved = next
			continue
		}
		links++
		if links > maxSymlinks {
			return "", fmt.Errorf("sandbox: %s: too many levels of symbolic links", path)
		}
		target, err := os.Readlink(next)
		if err != nil {
			return "", fmt.Errorf("sandbox: %w", err)
		}
		if filepath.IsAbs(target) {
			tvol := filepath.VolumeName(target)
			resolved = tvol + string(filepath.Separator)
			target = target[len(tvol):]
		}
		pending = append(splitPath(target), pending...)
	}
	return filepath.Clean(resolved), nil
}

func splitPath(p string) []string {
	return strings.FieldsFunc(p, func(r rune) bool { return os.IsPathSeparator(uint8(r)) })
}

// ResolveRulePath normalizes path the way Check does, so rule and checked paths
// compare as the same string. Rule paths must go through it at policy-build time: an
// unresolved rule matches nothing. A path normalizePath rejects comes back lexically
// clean.
func ResolveRulePath(path string) string {
	resolved, err := normalizePath(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return resolved
}

// Under reports whether child is at or beneath parent (both must be absolute and
// lexically clean). An empty parent contains nothing.
func Under(child, parent string) bool {
	if parent == "" {
		return false
	}
	if child == parent {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(parent, sep) {
		parent += sep
	}
	return strings.HasPrefix(child, parent)
}
