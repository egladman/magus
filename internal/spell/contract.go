package spell

// ContractEntry describes one optional entry in the mgs_ spell contract. The
// resolver (resolve.go) iterates OptionalContract, so the optional functions and
// the decoder keys they map to live in one canonical list rather than being
// spelled out at each call site.
type ContractEntry struct {
	Name     string // exported mgs_ function name
	Field    string // decoder field key the resolved value is stored under
	TakesDir bool   // true when the function accepts a directory string argument
}

// OptionalContract is the canonical list of optional mgs_ functions a spell
// module may export (mgs_getName is required and handled separately by the
// resolver). Resolve calls each present function and stores its result under
// Field. Treat as read-only.
//
// Every scalar and list contribution (needs, provides, claims, version_cmd,
// opaque) resolves uniformly. The "ops" entry (mgs_listTargets) is the exception:
// resolveOps post-processes it to extract function-valued op handlers into
// command records (the form the built-in spells use). Record-shaped ops pass
// through unchanged. See docs/engines.md.
var OptionalContract = []ContractEntry{
	{Name: "mgs_listRequiredGlobs", Field: "needs", TakesDir: true},
	{Name: "mgs_listProvidedGlobs", Field: "provides"},
	{Name: "mgs_listClaimedGlobs", Field: "claims"},
	{Name: "mgs_getVersionCommand", Field: "version_cmd"},
	{Name: "mgs_isOpaque", Field: "opaque"},
	{Name: "mgs_listTargets", Field: "ops"},
}
