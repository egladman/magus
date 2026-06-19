package types

// PatchOp is one RFC 6902 operation over a target's argv array. Path/From are
// single-token JSON Pointers (RFC 6901) into the array — "/N" for an index, or
// "/-" (add only) for the append position. Value is the string element for
// add/replace/test; From is the source pointer for move/copy. magus-types-gen
// mirrors it to the Buzz `object PatchOp`.
type PatchOp struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value,omitempty"`
	// From is the move/copy source JSON Pointer. The Buzz field is named fromPtr
	// because `from` is a reserved word in Buzz; the JSON stays "from" (RFC 6902).
	// Only the host charm.move/copy constructors set it — the pure-Buzz core never
	// does — so nothing references the Buzz name.
	From string `json:"from,omitempty" buzz:"fromPtr"`
}

// Charm declares how one active charm modifies a target's argv: an ordered RFC
// 6902 JSON Patch applied over the base. Charms are element-level — whole-document
// (root, empty-path) replacement is rejected by ValidatePatch — so multiple active
// charms compose without one wiping another. magus-types-gen mirrors it to the Buzz
// `object Charm`, and the magus/charm constructors return it.
type Charm struct {
	Ops []PatchOp `json:"ops,omitempty"`
}

// Run is the command a fork op forks: a program (Cmd), its argument vector (Args),
// and charm modifiers keyed by charm name. It is the single source of truth shared
// two ways: magus-types-gen mirrors it to the Buzz `object Run` a fork handler
// builds and hands to its cb callback, and the resolved spell Op embeds it. An
// empty Run (no Cmd) is the no-op marker.
type Run struct {
	Cmd    string           `json:"cmd,omitempty"`
	Args   []string         `json:"args,omitempty"`
	Charms map[string]Charm `json:"charms,omitempty"`
}

// SpellOp is a single dispatchable surface of a spell — one tool-native Operation
// (see docs/operations.md). An op either forks a declared command — the embedded
// Run's Cmd/Args run via PATH with no script VM, magus passing the command straight
// through to the tool — or, when Func is set, dispatches that exported spell
// function in-VM with the invoke request's Params. The two are mutually exclusive:
// an empty Func means a fork op (its Run.Cmd may itself be empty, for a no-op marker
// op). Capture makes the op's magusfile method return the {stdout, stderr, code, ok}
// record (the same shape os.exec returns) instead of void — for ops whose output
// is the point (a hash, a revision date) rather than a build action whose exit
// code is all that matters. It is Go-internal (the resolved op), not mirrored to Buzz.
type SpellOp struct {
	Run
	Capture bool `json:"capture,omitempty"`
	// Func names an exported spell function (Buzz/Teal) to dispatch in-VM with the
	// invoke request's Params, returning its Data. When empty the op is a fork
	// target (Run.Cmd/Args); when set, Run.Cmd is unused.
	Func string `json:"fn,omitempty"`
	// Doc is the handler function's documentation comment (see buzz Chunk.Doc),
	// surfaced by `magus describe` and enforced by `magus doctor` for local Buzz
	// spells. Empty for fork built-ins (their Doc is not serialized in bytecode)
	// and for Teal spells (no comment-capture). omitempty keeps it out of
	// BuiltinsHash so the cache key is unaffected.
	Doc string `json:"doc,omitempty"`
}
