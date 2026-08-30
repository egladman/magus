package spellruntime

// contractEntry describes one optional entry in the mgs_ spell contract. The
// resolver (resolve.go) iterates OptionalContract, so the optional functions and
// the decoder keys they map to live in one canonical list rather than being
// spelled out at each call site.
type contractEntry struct {
	Name  string // exported mgs_ function name
	Field string // decoder field key the resolved value is stored under
	// Shape is what the entry's Buzz value is made of, and therefore which reduction
	// the resolver runs before storing it.
	//
	// It lives HERE, on the entry, rather than in a switch over field names in
	// resolve.go, because a field name spelled in two places is a field name that can
	// be renamed in one. That is not hypothetical: renaming mgs_listManifests to
	// mgs_listVersionFiles updated this list, the decoder and all four spell sources,
	// and strings(1) confirmed the regenerated .bo exported the new name - but the
	// switch still said "manifests", so pathValues quietly stopped running, the Path
	// objects were never reduced to strings, and the decoded field came back EMPTY with
	// nothing pointing at the cause. The rename was reverted over it. One list means the
	// next rename carries this behavior along with it.
	//
	// An enum rather than a bool per shape: with three of them, a second bool beside the
	// first encodes "both set" as a reachable state that means nothing.
	Shape contractShape
}

// contractShape is the element type of a contract entry's Buzz list value.
type contractShape uint8

const (
	// ShapeStrs is the zero value: a [str] stored as-is.
	ShapeStrs contractShape = iota
	// ShapePaths is a [Path], reduced to a [str] by pathValues. The Path object's
	// other fields (base, isDir) are DISCARDED - the cache descriptor wants the
	// lexical value, because glob matching does not resolve filesystem paths.
	ShapePaths
	// ShapeManifests is a [Manifest], kept structured because its lockCandidates
	// field is data the descriptor needs, not decoration. A spell still returning
	// [Path] here decodes as manifests declaring no lockfile: both objects carry a
	// .value and the reduction reads keys structurally, so the pre-Manifest contract
	// keeps loading rather than failing.
	ShapeManifests
)

// OptionalContract is the canonical list of optional mgs_ functions a spell
// module may export (mgs_getName is required and handled separately by the
// resolver). Resolve calls each present function and stores its result under
// Field. Treat as read-only.
//
// MGS functions take no arguments. They run while Magus is discovering a spell,
// before there is a selected target or execution context; a target's magus.Context
// would therefore be fabricated data at this boundary. Per-invocation typed inputs
// belong on ordinary exported spell functions instead. Every scalar and list
// contribution (needs, provides, version_cmd, opaque) resolves uniformly.
// The "ops" entry (mgs_listTargets) is the exception:
// resolveOps post-processes it to extract function-valued op handlers into
// command records (the form the built-in spells use). Record-shaped ops pass
// through unchanged. See docs/engines.md.
var OptionalContract = []contractEntry{
	{Name: "mgs_listRequiredGlobs", Field: "needs", Shape: ShapePaths},
	{Name: "mgs_listProvidedGlobs", Field: "provides", Shape: ShapePaths},
	{Name: "mgs_listClaimedGlobs", Field: "claims", Shape: ShapePaths},
	{Name: "mgs_listIgnoreDirs", Field: "ignore_dirs", Shape: ShapePaths},
	{Name: "mgs_listManifests", Field: "manifests", Shape: ShapeManifests},
	{Name: "mgs_getTools", Field: "tools"},
	{Name: "mgs_getLanguage", Field: "language"},
	{Name: "mgs_isOpaque", Field: "opaque"},
	{Name: "mgs_listTargets", Field: "ops"},
}
