package watch

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/types"
)

// IgnoreFlag accumulates repeated --ignore values; satisfies flag.Value.
type IgnoreFlag struct {
	Patterns []types.IgnorePattern
}

func (f *IgnoreFlag) String() string {
	parts := make([]string, len(f.Patterns))
	for i, p := range f.Patterns {
		parts[i] = fmt.Sprintf("%s=%s", string(p.Type), p.Pattern)
	}
	return strings.Join(parts, ",")
}

func (f *IgnoreFlag) Set(value string) error {
	p, err := ParsePattern(value)
	if err != nil {
		return err
	}
	f.Patterns = append(f.Patterns, p)
	return nil
}

// ToWorkspaceRel converts an absolute path from the watcher to a
// workspace-relative slash-separated one. A relative path and any path that
// escapes the workspace root pass through verbatim.
func ToWorkspaceRel(wsRoot, p string) string {
	if !filepath.IsAbs(p) {
		return p
	}
	rel, err := filepath.Rel(wsRoot, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return p
	}
	return filepath.ToSlash(rel)
}
