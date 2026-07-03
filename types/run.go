package types

// PatchOp is one RFC 6902 operation over a target's argv array. Path/From are
// single-token JSON Pointers (RFC 6901) into the array — "/N" for an index, or
// "/-" (add only) for the append position. Value is the string element for
// add/replace/test; From is the source pointer for move/copy. magus-utils types
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
// charms compose without one wiping another. magus-utils types mirrors it to the Buzz
// `object Charm`, and the magus/charm constructors return it.
type Charm struct {
	Ops []PatchOp `json:"ops,omitempty"`
}

// Command is the declarative description of what a command op runs: a program on
// PATH (Bin), its argument vector (Args), and charm modifiers keyed by charm name.
// It describes what would run, not the running of it — the static form is what lets
// the argv be charm-patched, hashed into the cache key, and previewed by
// `magus describe` without executing. It is the single source of truth shared two
// ways: magus-utils types mirrors it to the Buzz `object Command` a spell op
// returns, and the resolved spell Op embeds it. An empty Command (no Bin) is the
// no-op marker.
type Command struct {
	Bin    string           `json:"bin,omitempty"`
	Args   []string         `json:"args,omitempty"`
	Charms map[string]Charm `json:"charms,omitempty"`
}

// Op kinds. A kind lives on the op, not the spell: one spell freely mixes command
// ops and service ops under one name. The kind is inferred from what the op handler
// returns - a [Command] (OpKindCommand) or a [Service] (OpKindService) - so
// authoring stays a single mgs_listTargets. Both are declarative data differing only
// in lifecycle (run-to-completion vs long-running), not the imperative handler split
// magus removed. An empty SpellOp.Kind means OpKindCommand.
const (
	OpKindCommand = "command"
	OpKindService = "service"
)

// Service is the declarative description of a long-running process a service op
// manages. Command (required) is the process. Run directly (`magus run <target>`) it
// is forked in the foreground and blocked on (Ctrl-C signals the child); reached as a
// dependency it is supervised in the background (see internal/service). Readiness and
// Stop are optional: Readiness is a probe polled until it exits 0 (how the supervisor
// learns the process is up and gates dependents on it), and Stop is a graceful-shutdown
// command run instead of signaling the process (also replayed by the daemon's crash
// reaper).
// Like [Command] each is static data - inspectable, cache-keyable, charm-patchable. It
// is a distinct return type (vs [Command]) so an op's kind is inferred from what it
// returns. magus-utils types mirrors it to the Buzz `object Service` a service op returns.
type Service struct {
	Command   Command `json:"command,omitempty"`
	Readiness Command `json:"readiness,omitempty"`
	Stop      Command `json:"stop,omitempty"`
	// Distinct, when non-empty, opts this service out of shared-instance dedup and
	// silences its near-duplicate (MGS5001) warning. It is a required reason string
	// (the golangci-lint nolintlint model): being distinct without a reason is
	// meaningless, so the reason IS the value. Recorded so `magus doctor` can audit
	// every deliberate divergence and flag reasons that no longer apply (a distinct
	// service with no remaining near-duplicate is a stale suppression).
	Distinct string `json:"distinct,omitempty"`
	// Idle overrides the per-service idle timeout (a duration like "30m") after which
	// the daemon reaps this shared service once its last dependent releases. Empty
	// uses the daemon's global default. Consumed by the service supervisor.
	Idle string `json:"idle,omitempty"`
}

// SpellOp is a single dispatchable surface of a spell — one tool-native Operation
// (see docs/operations.md). An op is one of two declarative shapes, tagged by Kind:
// a command op (OpKindCommand, the default) whose embedded [Command] Bin/Args run
// via PATH with no script VM; or a service op (OpKindService) whose [Service]
// describes a long-running process `magus run` blocks on. Either way the form is declarative,
// so the argv is charm-patched and rendered by `magus describe` without executing.
//
// For a service op the embedded Command mirrors Service.Command (the process), so
// every fork/render/cache path reads the op uniformly; `magus run` forks it in the
// foreground and blocks. Command.Bin may be empty, for a no-op marker op.
//
// (In-VM spell logic — API calls, a cache backend's get/put — is not an op kind: a
// remote cache backend is a separate contract magus's core invokes by name, and
// other custom logic belongs in a magusfile target body, not the operation model.)
//
// Capture makes the op's magusfile method return the {stdout, stderr, code, ok}
// record (the same shape os.exec returns) instead of void — for ops whose output
// is the point (a hash, a revision date) rather than a build action whose exit code
// is all that matters. It is Go-internal (the resolved op), not mirrored to Buzz.
type SpellOp struct {
	// Kind is the op's lifecycle kind (OpKind*); empty means OpKindCommand.
	Kind string `json:"kind,omitempty"`
	Command
	// Service is set only for a service op (Kind == OpKindService); nil otherwise.
	Service *Service `json:"service,omitempty"`
	Capture bool     `json:"capture,omitempty"`
	// Doc is the handler function's documentation comment (see buzz Chunk.Doc),
	// surfaced by `magus describe` and enforced by `magus doctor` for local Buzz
	// spells. Empty for command built-ins (their Doc is not serialized in bytecode).
	// omitempty keeps it out of BuiltinsHash so the cache key is unaffected.
	Doc string `json:"doc,omitempty"`
}

// OpKind returns the op's kind, resolving the empty default to OpKindCommand so
// callers dispatch on one canonical value.
func (o SpellOp) OpKind() string {
	if o.Kind == "" {
		return OpKindCommand
	}
	return o.Kind
}

// IsService reports whether the op is a service op (a long-running process) rather
// than a command op (run to completion).
func (o SpellOp) IsService() bool { return o.Kind == OpKindService }
