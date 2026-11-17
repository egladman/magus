package unverified

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// GlobSets numbers the distinct glob sets a workspace's targets declare and keeps, per
// file, which of them claim it. Targets of one project often share its sources, so one
// set stands for all of them. The zero value is empty and ready to use; it is not safe
// for concurrent use.
type GlobSets struct {
	sets    [][]types.Glob
	ids     map[string]int
	claimed map[string][]bool
}

// Add is the index of a set matching as globs do, added when none does. Add every set
// before the first Claiming: an answer Claiming keeps does not grow with later sets.
func (s *GlobSets) Add(globs []types.Glob) int {
	var key strings.Builder
	for _, g := range globs {
		key.WriteString(g.Pattern)
		for _, e := range g.Except {
			key.WriteString("\x01" + e)
		}
		key.WriteString("\x00")
	}
	if s.ids == nil {
		s.ids = map[string]int{}
	}
	id, ok := s.ids[key.String()]
	if !ok {
		id = len(s.sets)
		s.sets = append(s.sets, globs)
		s.ids[key.String()] = id
	}
	return id
}

// Claiming reports, for each set by its Add index, whether it claims rel, a
// workspace-relative path.
func (s *GlobSets) Claiming(rel string) []bool {
	if got, ok := s.claimed[rel]; ok {
		return got
	}
	got := make([]bool, len(s.sets))
	for i, set := range s.sets {
		got[i] = slices.ContainsFunc(set, func(g types.Glob) bool { return g.Match(rel) })
	}
	if s.claimed == nil {
		s.claimed = map[string][]bool{}
	}
	s.claimed[rel] = got
	return got
}

// WorkspacePath is file, relative to the checkout's top level root, made relative to
// workspace inside it; false when file lies outside workspace.
func WorkspacePath(root, workspace, file string) (string, bool) {
	prefix, err := filepath.Rel(root, workspace)
	if err != nil {
		return "", false
	}
	if prefix = filepath.ToSlash(prefix); prefix == "." {
		return file, true
	}
	return strings.CutPrefix(file, prefix+"/")
}
