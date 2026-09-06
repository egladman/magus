package types

import "slices"

// ProjectOption is one recognized magus.project({...}) key and the release that first
// understood it.
//
// Since is what lets a workspace say "this magusfile needs 0.5.0" before an older binary
// meets a key it cannot act on. Load no longer aborts over such a key (hint.CheckKeys),
// so the failure it prevents is quieter now and worth more: the key is dropped, the
// policy it declared does not apply, and only the declared floor says why.
//
// doctor's "required version covers schema" check asserts the floor covers the keys in
// use, and internal/doctor.usedSchemaKeys must be able to detect every key carrying one.
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

// TargetPolicyOptions is the ONE list of recognized keys inside magus.project's
// "targets" map, for the reason ProjectOptions above is one list.
//
// The dimension the project-option fix did not cover, so it recurred here: the dry-run
// host carried a hand-copied second list that had drifted to three keys, and a
// magusfile setting memory_mb, cache, drift or drift_reason passed a real run and was
// rejected by its own preview.
//
// This carried plain names and no Since column, on the reasoning that a floor is only
// worth declaring where doctor can DETECT the key in use and nothing decoded carried
// target policy. Both halves were wrong by the time they mattered: Project.TargetPolicies
// carries exactly that state, and this vocabulary grew twice in three merges, with
// `timeout` deadlocking a workspace whose binary predated it.
var TargetPolicyOptions = []ProjectOption{
	{Key: "skip_cache"},
	{Key: "exclusive"},
	{Key: "slots"},
	{Key: "memory_mb"},
	{Key: "cache"},
	{Key: "drift"},
	{Key: "drift_reason"},
	{Key: "timeout", Since: "0.5.0"},
	{Key: "retry_on_volatile", Since: "0.5.0"},
}

// ToolBoundKeys is the ONE list of recognized keys inside one entry of magus.project's
// "tools" map, the version window a project requires of a bin.
//
// The third dimension of the same divergence, found permissive rather than strict: the
// engine rejected an unknown bound key and the dry-run host walked "tools" not at all,
// so `{"go": {"minn": "1.21"}}` passed the Playground and every other preview surface
// and then failed the real run. A shared table makes both sides answer alike.
//
// No Since column because both members shipped with the mechanism. Add one the moment a
// third arrives; "min/below is closed and cannot grow" was the same claim made about
// target policy, which grew twice.
var ToolBoundKeys = []string{
	"min",
	"below",
}

// ProjectOptionKeys returns just the key names, for the unknown-key check both the
// engine and the dry-run host perform.
func ProjectOptionKeys() []string { return optionKeys(ProjectOptions) }

// TargetPolicyKeys returns just the key names of TargetPolicyOptions, for the same
// check one level down.
func TargetPolicyKeys() []string { return optionKeys(TargetPolicyOptions) }

func optionKeys(opts []ProjectOption) []string {
	out := make([]string, 0, len(opts))
	for _, o := range opts {
		out = append(out, o.Key)
	}
	return out
}

// ProjectOptionSince returns the release that first understood key, and whether the key
// is recognized at all. An empty version for a recognized key means it predates floors.
func ProjectOptionSince(key string) (string, bool) { return optionSince(ProjectOptions, key) }

// TargetPolicySince is ProjectOptionSince for a per-target policy key.
//
// Separate from ProjectOptionSince rather than one merged lookup: `exclusive` is a
// member of BOTH vocabularies, so a single table keyed by name could only answer for
// one of them.
func TargetPolicySince(key string) (string, bool) { return optionSince(TargetPolicyOptions, key) }

func optionSince(opts []ProjectOption, key string) (string, bool) {
	i := slices.IndexFunc(opts, func(o ProjectOption) bool { return o.Key == key })
	if i < 0 {
		return "", false
	}
	return opts[i].Since, true
}
