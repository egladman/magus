package filesystem

import (
	"os"
	"path/filepath"
)

// ExpandUserRule resolves a magus.yaml sandbox.allow entry to a Rule.
// rawPath may use ~ for the user's home directory and may contain $VAR
// references resolved against the current environment.
// User-allowlisted paths default to Exec: true so that toolchain directories
// (e.g. $GOPATH/bin, $CARGO_HOME/bin) remain executable, matching the
// behavior before the Read/Exec access split was introduced.
func ExpandUserRule(rawPath string, read, write bool) (Rule, error) {
	expanded := os.ExpandEnv(rawPath)
	if len(expanded) > 0 && expanded[0] == '~' {
		home, err := os.UserHomeDir()
		if err == nil {
			expanded = filepath.Join(home, expanded[1:])
		}
	}
	abs, err := filepath.Abs(expanded)
	if err != nil {
		return Rule{}, err
	}
	clean := filepath.Clean(abs)
	// Resolve symlinks so the containment check is consistent with how the
	// workspace root is stored. If the path does not exist yet, keep the
	// lexical form (same tolerance as normalizePath).
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = resolved
	}
	return Rule{Path: clean, Read: read, Write: write, Exec: true}, nil
}
