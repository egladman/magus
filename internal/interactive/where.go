package interactive

import (
	"fmt"
	"io"
	"path/filepath"
	"slices"

	"github.com/egladman/magus/internal/file"
)

// WhereMatch is one resolved location. Rel is carried alongside the absolute Path
// because a caller that resolved a name usually needs it back in workspace-relative
// form to hand to another magus command, and re-deriving it means knowing the root.
type WhereMatch struct {
	Path string `json:"path"  yaml:"path"`
	Rel  string `json:"rel"   yaml:"rel"`
	Kind string `json:"kind"  yaml:"kind"` // "project" or "file"
}

// WhereOutput is the structured view of a where resolution.
type WhereOutput struct {
	Workspace string       `json:"workspace" yaml:"workspace"`
	Count     int          `json:"count"     yaml:"count"`
	Matches   []WhereMatch `json:"matches"   yaml:"matches"`
}

// FileMatches resolves scored file hits against the workspace root.
func FileMatches(wsRoot string, files []ScoredFile) []WhereMatch {
	matches := make([]WhereMatch, 0, len(files))
	for _, f := range files {
		matches = append(matches, WhereMatch{Path: filepath.Join(wsRoot, f.Path), Rel: f.Path, Kind: "file"})
	}
	return matches
}

// ProjectMatches resolves scored project hits against the workspace root.
func ProjectMatches(wsRoot string, scored []ScoredProject) []WhereMatch {
	matches := make([]WhereMatch, 0, len(scored))
	for _, s := range scored {
		matches = append(matches, WhereMatch{Path: filepath.Join(wsRoot, s.P.Path), Rel: s.P.Path, Kind: "project"})
	}
	return matches
}

// PrintWherePaths writes one absolute path per line and nothing else: the text form of
// `magus where` is substituted straight into another command.
func PrintWherePaths(w io.Writer, matches []WhereMatch) {
	for _, m := range matches {
		fmt.Fprintln(w, m.Path)
	}
}

// NormalizeFilters canonicalises the path-SHAPED filters to the workspace-relative slash
// form both matchers compare against, so a filter a human tab-completed ("./cmd/x") or
// copied out of an editor (an absolute path) reaches what a bare "cmd/x" reaches. It is
// the same normalization `magus query` applies to a path-shaped term.
//
// The result is a copy: the filters the user typed are what the no-match error reports.
func NormalizeFilters(filters []string, wsRoot string) []string {
	out := slices.Clone(filters)
	for i, f := range out {
		if norm, ok := file.NormalizeWorkspacePath(f, wsRoot); ok {
			out[i] = norm
		}
	}
	return out
}
