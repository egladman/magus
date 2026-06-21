package types

// PatchOp is one RFC 6902 operation over a target's argv array. Path/From are
// single-token JSON Pointers (RFC 6901) into the array — "/N" for an index, or
// "/-" (add only) for the append position. Value is the string element for
// add/replace/test; From is the source pointer for move/copy. magus-scribe types
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
// charms compose without one wiping another. magus-scribe types mirrors it to the Buzz
// `object Charm`, and the magus/charm constructors return it.
type Charm struct {
	Ops []PatchOp `json:"ops,omitempty"`
}

// Run is the command a command op runs: a program (Cmd), its argument vector
// (Args), and charm modifiers keyed by charm name. It is the single source of
// truth shared two ways: magus-scribe types mirrors it to the Buzz `object Run` a
// command handler builds and hands to its cb callback, and the resolved spell Op
// embeds it. An empty Run (no Cmd) is the no-op marker.
//
// Despite the name, Run is a command/invocation spec (what to run), not part of
// the run subsystem (the execution machinery that runs targets); the two are
// unrelated.
type Run struct {
	Cmd    string           `json:"cmd,omitempty"`
	Args   []string         `json:"args,omitempty"`
	Charms map[string]Charm `json:"charms,omitempty"`
}

// OpKind is the dispatch strategy of a [SpellOp].
type OpKind int

const (
	// OpCommand runs the op's declared command (the embedded [Run]) as an external
	// process via PATH — declarative data: its argv is charm-patchable and can be
	// rendered by `magus describe` without executing it.
	OpCommand OpKind = iota
	// OpHandler invokes the op's named in-VM function with the request's Params —
	// imperative spell logic (API calls, branching) that no single command expresses.
	OpHandler
)

// SpellOp is a single dispatchable surface of a spell — one tool-native Operation
// (see docs/operations.md). Its [SpellOp.Kind] is either:
//
//   - OpCommand: the embedded [Run]'s Cmd/Args run via PATH with no script VM,
//     magus passing the command straight through to the tool (Run.Cmd may itself
//     be empty, for a no-op marker op); or
//   - OpHandler: the exported function named by Func is dispatched in-VM with the
//     invoke request's Params.
//
// The two are mutually exclusive: a set Func selects OpHandler, otherwise the op
// is an OpCommand. Capture makes the op's magusfile method return the
// {stdout, stderr, code, ok} record (the same shape os.exec returns) instead of
// void — for ops whose output is the point (a hash, a revision date) rather than a
// build action whose exit code is all that matters. It is Go-internal (the
// resolved op), not mirrored to Buzz.
type SpellOp struct {
	Run
	Capture bool `json:"capture,omitempty"`
	// Func names an exported spell function to dispatch in-VM with the invoke
	// request's Params, returning its Data — i.e. the op is an OpHandler. When
	// empty the op is an OpCommand (Run.Cmd/Args); when set, Run.Cmd is unused.
	Func string `json:"fn,omitempty"`
	// Doc is the handler function's documentation comment (see buzz Chunk.Doc),
	// surfaced by `magus describe` and enforced by `magus doctor` for local Buzz
	// spells. Empty for command built-ins (their Doc is not serialized in bytecode).
	// omitempty keeps it out of BuiltinsHash so the cache key is unaffected.
	Doc string `json:"doc,omitempty"`
}

// Kind reports how the op is dispatched: OpHandler when it names an in-VM
// function, else OpCommand. It is the one place the command/handler distinction is
// decided, so call sites read Kind() rather than testing Func directly.
func (o SpellOp) Kind() OpKind {
	if o.Func != "" {
		return OpHandler
	}
	return OpCommand
}
