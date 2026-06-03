package spell

// ContractEntry describes one optional entry in the engine-agnostic mgs_ spell
// contract. The Buzz resolver (resolve.go) iterates OptionalContract, so the
// optional functions and the decoder keys they map to live in one canonical
// list rather than being spelled out at each call site.
type ContractEntry struct {
	Name     string // exported mgs_ function name
	Field    string // decoder field key the resolved value is stored under
	TakesDir bool   // true when the function accepts a directory string argument
}

// OptionalContract is the canonical list of optional mgs_ functions a spell
// module may export (mgs_getName is required and handled separately by each
// resolver). Both resolvers call each present function and store its result under
// Field. Treat as read-only.
//
// The two paths resolve every scalar and list contribution identically (needs,
// provides, claims, version_cmd, opaque). The "ops" entry (mgs_listTargets)
// differs in post-processing: the Buzz path additionally runs resolveOps to
// extract function-valued op handlers into command records (the form the
// built-in spells use), whereas Teal spells declare record-shaped ops directly
// and function-valued ops are ignored. See docs/engines.md.
var OptionalContract = []ContractEntry{
	{Name: "mgs_listRequiredGlobs", Field: "needs", TakesDir: true},
	{Name: "mgs_listProvidedGlobs", Field: "provides"},
	{Name: "mgs_listClaimedGlobs", Field: "claims"},
	{Name: "mgs_getVersionCommand", Field: "version_cmd"},
	{Name: "mgs_isForeignProcess", Field: "opaque"},
	{Name: "mgs_listTargets", Field: "ops"},
}
