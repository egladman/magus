package types

import "errors"

// AffectedResult is the outcome of a workspace affected-set computation.
type AffectedResult struct {
	Base        string              // ref used for the diff
	Changed     []string            // repo-relative changed paths
	Seed        []string            // project paths that contain changed files
	FilesBySeed map[string][]string // seed → changed files within it
	Affected    []string            // transitive reverse closure of Seed, sorted
	// UndeclaredBySeed is the subset of FilesBySeed that NO project's declared globs
	// name. Those files reached their seed through directory containment alone (the
	// root catch-all included), so they rerun that project's targets without moving
	// any cache key: the run is real work whose result was already correct.
	//
	// Carried rather than recomputed because every consumer of FilesBySeed asks the
	// same follow-up - `--impact` qualifies its "seeded by N changed files" with it,
	// and MGS1028 reports it - and the declarations it is derived from are not in
	// reach once the result has crossed out of the workspace.
	UndeclaredBySeed map[string][]string
}

// ErrAffectedFallback is returned when the VCS cannot compute a definitive changed-files set.
var ErrAffectedFallback = errors.New("affected: cannot compute affected set")
