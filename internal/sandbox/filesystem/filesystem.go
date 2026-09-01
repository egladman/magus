// Package filesystem holds the filesystem half of a sandbox policy: the path
// allowlist (Ruleset) and path-shape checks consulted before touching the filesystem.
package filesystem

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ErrDenied is returned by Ruleset.CheckRead, CheckWrite, and CheckExec when a
// requested operation falls outside the configured allowlist.
var ErrDenied = errors.New("sandbox: operation denied by the sandbox policy")

// accessMode classifies a requested access against the ruleset.
type accessMode int

const (
	modeRead  accessMode = iota // read-only
	modeWrite                   // write or create
	modeExec                    // execute (binary launch)
)

// Rule is one entry in the policy's filesystem allowlist.
//
// Exec is the KERNEL layer's input: landlock grants execve where it is set, and Read
// alone is enough for dlopen/mmap without permitting execve. checkAccess below does
// not consult it - CheckExec passes on Read, and TestCheckExecRequiresReadNotExec
// pins that. So on a host with no landlock (darwin, or a kernel below 5.13) a
// read-only rule does not stop an exec, and Exec: false on a rule is a request the
// kernel layer carries out or nobody does.
type Rule struct {
	Path  string
	Read  bool
	Write bool
	Exec  bool
}

// Ruleset is the filesystem allowlist consulted by CheckRead/CheckWrite/CheckExec.
type Ruleset struct {
	Rules []Rule
}

// CheckRead reports whether the ruleset permits a read of path (must be absolute; path-shape only).
func (rs Ruleset) CheckRead(path string) error {
	return rs.checkAccess(path, modeRead)
}

// CheckWrite reports whether the ruleset permits a write to path (must be absolute; path-shape only).
func (rs Ruleset) CheckWrite(path string) error {
	return rs.checkAccess(path, modeWrite)
}

// CheckExec reports whether the ruleset permits execution of path. Does not resolve $PATH; use exec.LookPath first.
func (rs Ruleset) CheckExec(path string) error {
	return rs.checkAccess(path, modeExec)
}

func (rs Ruleset) checkAccess(path string, mode accessMode) error {
	abs, err := normalizePath(path)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrDenied, path, err)
	}
	for _, r := range rs.Rules {
		if !under(abs, r.Path) {
			continue
		}
		switch mode {
		case modeRead, modeExec:
			if r.Read {
				return nil
			}
		case modeWrite:
			if r.Write {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %s outside workspace allowlist", ErrDenied, abs)
}

// normalizePath returns an absolute, symlink-resolved, lexically-clean path.
// For non-existent paths (write targets), resolves the parent and re-attaches the base name.
// On Linux ≥5.13 the landlock layer closes the residual TOCTOU window; on other platforms it is accepted.
func normalizePath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("sandbox: empty path")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("sandbox: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}
	// The path does not exist yet (a write target). Walk up to the nearest ancestor
	// that DOES exist, resolve that, and re-attach the missing tail.
	//
	// COST: this branch is roughly 3x the resolved one - 150 allocs/op and ~23us against
	// 47 allocs/op and ~8.3us (BenchmarkCheckReadCtx/missing vs /existing in
	// internal/sandbox, Apple M5 darwin/arm64, 6 runs). Treat the ratio as the durable
	// figure; the absolute numbers track TMPDIR depth, because the dominant cost in BOTH
	// branches is EvalSymlinks lstat-ing every path component, and this branch calls it
	// once per ancestor instead of once.
	//
	// It matters because callers hit it in loops: fs.glob consults CheckReadCtx once per
	// match, so a check over paths not on disk pays the multiplier per path. Filter to
	// paths that exist before checking, or memoize per run - resolution is pure for a
	// given tree.
	//
	// Resolving only the immediate parent was not enough: when the parent is also missing,
	// the fallback kept the whole path lexical, so a symlink above it went unresolved.
	// Rule paths ARE resolved, so the two forms could never match and the write was
	// denied - every nested create on a workspace under a symlink, which on macOS is any
	// path under /var or /tmp.
	missing := []string{}
	dir := abs
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			break // reached the root without finding anything that exists
		}
		missing = append([]string{filepath.Base(dir)}, missing...)
		if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Clean(filepath.Join(append([]string{resolvedParent}, missing...)...)), nil
		}
		dir = parent
	}
	return filepath.Clean(abs), nil
}

// ResolveRulePath normalizes path the same way checkAccess does so containment checks are symmetric.
// Rule paths must be normalized at policy-build time; divergence from the kernel landlock layer is a security hazard.
func ResolveRulePath(path string) string {
	resolved, err := normalizePath(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return resolved
}

// under reports whether child is at or beneath parent (both must be absolute and lexically clean).
func under(child, parent string) bool {
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
