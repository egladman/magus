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
	{Key: "review_required", Since: "0.5.0"},
	{Key: "gate_low_risk", Since: "0.5.0"},
	{Key: "gate_inherit", Since: "0.5.0"},
}

// TargetPolicyKeys is the ONE list of recognized keys inside magus.project's "targets"
// map, for the reason ProjectOptions above is one list.
//
// The dimension the project-option fix did not cover, so it recurred here: the dry-run
// host carried a hand-copied second list that had drifted to three keys, and a
// magusfile setting memory_mb, cache, drift or drift_reason passed a real run and was
// rejected by its own preview.
//
// Plain names, no Since column: a floor is only worth declaring where doctor can DETECT
// the key in use, and it detects from decoded Project state (internal/doctor/
// schemafloor.go), which carries no target policy. Grow this into a record when that
// detection exists, not before.
var TargetPolicyKeys = []string{
	"skip_cache",
	"exclusive",
	"slots",
	"memory_mb",
	"timeout",
	"cache",
	"drift",
	"drift_reason",
	"retry_on_volatile",
}

// ToolBoundKeys is the ONE list of recognized keys inside one entry of magus.project's
// "tools" map, the version window a project requires of a bin.
//
// The third dimension of the same divergence, found permissive rather than strict: the
// engine rejected an unknown bound key and the dry-run host walked "tools" not at all,
// so `{"go": {"minn": "1.21"}}` passed the Playground and every other preview surface
// and then failed the real run. A shared table makes both sides answer alike.
//
// No Since column, for the reason TargetPolicyKeys has none. A closed vocabulary
// besides: min/below describes a half-open interval and gains no third member, which is
// why both consumers reject against it plainly instead of hinting at an upgrade.
var ToolBoundKeys = []string{
	"min",
	"below",
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
