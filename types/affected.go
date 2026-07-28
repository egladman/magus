package types

import "errors"

// AffectedResult is the outcome of a workspace affected-set computation.
type AffectedResult struct {
	Base        string              // ref used for the diff
	Changed     []string            // repo-relative changed paths
	Seed        []string            // project paths that contain changed files
	FilesBySeed map[string][]string // seed → changed files within it
	Affected    []string            // transitive reverse closure of Seed, sorted
}

// ErrAffectedFallback is returned when the VCS cannot compute a definitive changed-files set.
var ErrAffectedFallback = errors.New("affected: cannot compute affected set")

// ToMap is the Buzz boundary map magus.affected returns. FilesBySeed is flattened
// to a map of lists so a magusfile can index it by seed path directly.
func (r AffectedResult) ToMap() map[string]any {
	filesBySeed := make(map[string]any, len(r.FilesBySeed))
	for seed, files := range r.FilesBySeed {
		filesBySeed[seed] = files
	}
	return map[string]any{
		"base":        r.Base,
		"changed":     r.Changed,
		"seed":        r.Seed,
		"filesBySeed": filesBySeed,
		"affected":    r.Affected,
	}
}
