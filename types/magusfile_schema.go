package types

import "slices"

// ProjectOption is one recognized magus.project({...}) key and the release that first
// understood it.
//
// Since exists because a magusfile key is a HARD compatibility break in a way a
// magus.yaml key is not. An unknown yaml key is a warning and the run continues; an
// unknown magus.project key aborts workspace load, so every magus command fails at
// once - including the one that would build a binary new enough to read the file. The
// only thing that turns that into a sentence instead of a puzzle is the workspace
// declaring a floor that covers the keys it actually uses, which is what doctor's
// "required version covers schema" check asserts using this field.
//
// Empty Since means the key predates the floor mechanism itself and needs no coverage.
type ProjectOption struct {
	Key   string
	Since string
}

// ProjectOptions is the ONE list of recognized magus.project keys.
//
// One list because there were two: the engine's and a parallel copy in the dry-run
// host, which had already drifted - the dry copy silently rejected a key the engine
// accepted, so a magusfile could pass a real run and fail a preview. A shared table in
// a near-leaf package is the only shape where that cannot recur.
var ProjectOptions = []ProjectOption{
	{Key: "name"},
	{Key: "depends_on"},
	{Key: "outputs"},
	{Key: "sources"},
	{Key: "exclusive"},
	{Key: "spells"},
	{Key: "watch_ignore"},
	{Key: "targets"},
	{Key: "no_language", Since: "0.4.0"},
	{Key: "tools", Since: "0.4.0"},
}

// ProjectOptionKeys returns just the key names, for the unknown-key rejection both the
// engine and the dry-run host perform.
func ProjectOptionKeys() []string {
	out := make([]string, 0, len(ProjectOptions))
	for _, o := range ProjectOptions {
		out = append(out, o.Key)
	}
	return out
}

// ProjectOptionSince returns the release that first understood key, and whether the key
// is recognized at all. An empty version for a recognized key means it predates floors.
func ProjectOptionSince(key string) (string, bool) {
	i := slices.IndexFunc(ProjectOptions, func(o ProjectOption) bool { return o.Key == key })
	if i < 0 {
		return "", false
	}
	return ProjectOptions[i].Since, true
}
