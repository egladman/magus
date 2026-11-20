package guard

import (
	"path/filepath"
	"strings"
)

// narrowerThanRoot reports a path operand proven to name less than
// the workspace root. It fails closed: an empty word, a wildcard or
// colon in the first component (`:/`, `glob:**`) counts as the root.
func narrowerThanRoot(word string, s workspaceScope) bool {
	if word == "" {
		return false
	}
	first, _, _ := strings.Cut(filepath.ToSlash(word), "/")
	if strings.ContainsAny(first, "*?[:") {
		return false
	}
	if clean := filepath.Clean(word); !filepath.IsAbs(word) && !strings.HasPrefix(word, "~") {
		if clean == "." {
			return false
		}
		if filepath.IsLocal(clean) {
			return true
		}
	}
	placed, ok := placePath(word, s.dir, s.home)
	if !ok || s.root == "" {
		return false
	}
	rel, err := filepath.Rel(canonicalPath(placed), canonicalPath(s.root))
	return err == nil && !filepath.IsLocal(rel)
}

// pathOperands are every word after `--`, and each non-flag before.
func pathOperands(args []string) []string {
	var out []string
	for i, a := range args {
		if a == "--" {
			return append(out, args[i+1:]...)
		}
		if !strings.HasPrefix(a, "-") {
			out = append(out, a)
		}
	}
	return out
}

// coversRoot reports an operand not proven narrower than the root.
func coversRoot(operands []string, s workspaceScope) bool {
	for _, o := range operands {
		if !narrowerThanRoot(o, s) {
			return true
		}
	}
	return false
}
