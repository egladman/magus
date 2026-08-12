package spells

import "strings"

// PatchOp is one RFC 6902 operation over a target's argv array. Path/From are
// single-token JSON Pointers (RFC 6901) into the array — "/N" for an index, or
// "/-" (add only) for the append position. Value is the string element for
// add/replace/test; From is the source pointer for move/copy. magus-utils types
// mirrors it to the Buzz `object PatchOp`.
type PatchOp struct {
	Op    PatchOpKind `json:"op"`
	Path  string      `json:"path"`
	Value string      `json:"value,omitempty"`
	// From is the move/copy source JSON Pointer. The Buzz field is named fromPtr
	// because `from` is a reserved word in Buzz, so no mirror can declare it; the JSON
	// stays "from" (RFC 6902). Everything on the Buzz side of the boundary - the charm
	// constructors that emit it, the decoder that reads it back, and the mirror - uses
	// fromPtr. They did not always agree: the constructors emitted "from", which the
	// mirror said was called fromPtr, so an annotated charm would have read an empty
	// field. Nothing caught it because nothing referenced the Buzz name.
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
//
// Bin or any Args entry may be a bare $NAME token (e.g. "$MAGUS") to reference a
// value the RUNNER computes for this invocation - the engine-side replacement
// for a spell shelling out to `sh -c` just to let the shell expand a variable
// magus itself set. See internal/interp/bindings' resolveRunnerRefs for the
// resolution rule and, importantly, its scope: it is NOT the process
// environment.
type Command struct {
	Bin    string           `json:"bin,omitempty"`
	Args   []string         `json:"args,omitempty"`
	Charms map[string]Charm `json:"charms,omitempty"`
	// Sources, when non-empty, are doublestar globs (relative to the project
	// directory this command runs in) that the RUNNER expands into a file list
	// at EXECUTION time, via the same walk that builds the cache key
	// (cache.ExpandSources) - so a Sources-declaring op inherits the workspace's
	// declared ignore dirs (the core project.IgnoreDirs plus the issuing spell's
	// own mgs_listIgnoreDirs) instead of hardcoding directory names.
	//
	// This exists because an op handler runs ONCE with a null Target and is
	// reduced to a static {bin, args} record (see recordOp in
	// internal/spellruntime/resolve.go) - it cannot walk a project directory
	// itself, which is why a spell used to shell out to `find | xargs` to build
	// its own file list. Declaring Sources here defers that walk to the runner,
	// per project, at run time, with no shell involved.
	//
	// The expanded files are appended to Args (after charm-patching and any
	// caller-supplied extra args) in one of two shapes, chosen by SourcesEach:
	// batched (the default: every match on as few invocations as fit under the
	// runner's ARG_MAX-safe limit) or one invocation per file. A glob set that
	// matches nothing runs the command ZERO times and reports success - the
	// engine-side equivalent of `xargs -r` - rather than invoking Bin with an
	// empty file list or failing outright.
	//
	// Empty (the default) leaves Args exactly as declared: no behavior change
	// for a Command that does not use this.
	Sources []string `json:"sources,omitempty"`
	// SourcesEach runs Bin once PER file Sources matches (xargs -n1: one file,
	// one invocation), each invocation appending that single file to Args. False
	// (the default) batches every matched file across as few invocations as fit
	// under the runner's ARG_MAX-safe limit. Meaningless without Sources.
	SourcesEach bool `json:"sources_each,omitempty"`
	// Capture makes this command's spell method return its exec record. The field
	// belongs on Command because it is declared by a Command-returning handler;
	// Op carries the resolved copy that dispatch reads.
	Capture bool `json:"capture,omitempty"`
	// Secrets declares the environment this command needs, as env var name -> provider
	// reference: {"NPM_TOKEN": "NPM_TOKEN"} means "set $NPM_TOKEN in this child from
	// whatever the workspace's secret provider resolves that reference to". A command
	// op's argv is static (see the type doc above), so it is the one op shape that can
	// never read magus\secret.read from a magusfile body - and a "provided" project (a
	// workspace provider, no magusfile at all) cannot reach a magusfile body either.
	// This is the declarative escape hatch for both: the refs are resolved through the
	// same secret.Resolver a magusfile uses, at spawn, and injected into ONLY this one
	// child process's environment - never into Args, never logged, never returned by
	// `magus describe`. Resolver.Read registers every value it hands back for
	// redaction (internal/secret), so a secret that reaches a child via this field is
	// masked out of captured output exactly as one read in a magusfile body is. Charms
	// cannot patch this field; they patch Args only. Refs are static data (never a
	// value), so the op stays hashable and describable without ever holding a secret.
	Secrets map[string]string `json:"secrets,omitempty"`
	// Hints classify a FAILURE of this command into a next step. Each entry pairs a
	// substring of the tool's output with the advice magus prints when the command
	// exits non-zero and that substring appeared. The first declared match wins; a
	// command that succeeds never consults them.
	//
	// This exists so a tool's own error can teach its fix. `docker buildx build --push`
	// failing with "authentication required" is a complete diagnosis to anyone who
	// already knows docker, and an exit code to everyone else - and the alternative
	// magus used to document (a separate `-login` target you must remember) is a mode
	// switch the user has to know about in advance, which is the thing advice removes.
	//
	// `json:"-"` is load-bearing, not tidiness. BuiltinsHash marshals the whole
	// resolved registry into every project's SpellDefVersion, so a field serialized
	// here puts its CONTENTS in every cache key: rewording a sentence of advice would
	// invalidate every target in every project, for a string that cannot change what
	// any command does. Doc is excluded from the key for the same reason (see below);
	// this is the same property, and JSON is not used to transport an Op - the only
	// marshal of the registry is the hash itself.
	Hints []Hint `json:"-"`
}

// SourcesPlaceholder renders Sources as a single human-readable argv token, for a
// renderer that cannot execute the runner's real expansion - `magus describe` and
// the dry-run preview both render a Command outside any project directory, so
// neither can walk Sources into real files the way runCommand does at execution
// time. nil when Sources is unset, so an ordinary Command's rendered argv is
// completely unchanged.
func (c Command) SourcesPlaceholder() []string {
	if len(c.Sources) == 0 {
		return nil
	}
	mode := "batch"
	if c.SourcesEach {
		mode = "each"
	}
	return []string{"<sources:" + mode + " " + strings.Join(c.Sources, ",") + ">"}
}

// Hint is one failure classification: when a command fails and Contains appears in its
// output, magus prints Advise.
//
// The field names are the authoring surface. A spell writes these in Buzz, so they are
// the same words in Go, in the generated mirror, and in a spell file:
//
//	Hint{contains = "authentication required", advise = "run `docker login <registry>`"}
//
// Contains rather than the more obvious "match" for two reasons that point the same way.
// It names the actual operation - this is strings.Contains, not a pattern - so the type
// no longer needs a paragraph insisting it is not a regex. And `match` is a RESERVED word
// in Buzz (libs/gopherbuzz parser reservedIdents), so a field called match can only be
// written `@"match" = ...` by every author forever. PatchOp.From above hit the same wall
// and solved it with a differing Buzz name, which then drifted from the Go name and
// silently produced empty fields; keeping one name in all three places is what avoids
// repeating that.
type Hint struct {
	// Contains is matched against the failed command's stdout and stderr SEPARATELY,
	// never against the two joined: a substring spanning the seam of two independent
	// streams would fire on output that appeared in neither.
	Contains string `json:"contains"`
	// Advise is the text magus prints. Write the command to run rather than the
	// diagnosis - it is printed after the tool's own error, which already said what
	// went wrong.
	Advise string `json:"advise"`
}

// Op kinds. A kind lives on the op, not the spell: one spell freely mixes command
// ops and service ops under one name. The kind is inferred from what the op handler
// returns - a [Command] (OpKindCommand) or a [Service] (OpKindService) - so
// authoring stays a single mgs_listTargets. Both are declarative data differing only
// in lifecycle (run-to-completion vs long-running), not the imperative handler split
// magus removed. An empty Op.Kind means OpKindCommand.
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

// Op is a single dispatchable surface of a spell — one tool-native Operation
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
// record (the same shape proc.exec returns) instead of void — for ops whose output
// is the point (a hash, a revision date) rather than a build action whose exit code
// is all that matters. It is Go-internal (the resolved op), not mirrored to Buzz.
type Op struct {
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
func (o Op) OpKind() string {
	if o.Kind == "" {
		return OpKindCommand
	}
	return o.Kind
}

// IsService reports whether the op is a service op (a long-running process) rather
// than a command op (run to completion).
func (o Op) IsService() bool { return o.Kind == OpKindService }

// Key returns the lines identifying this op's work for the cache: the command it runs, which is
// the honest answer to "what work is this" and what lets two entry points onto the same
// op share an entry.
//
// A service op contributes nothing, and that is correct rather than a gap: a
// service-backed target is forced NoCache at run.go's step construction, so it is never
// replayed and has no key to protect. A function-op computes its argv in-VM, so an empty
// Bin likewise has nothing to say.
func (o Op) Key() []string {
	// IsService first: decode mirrors a service op's command onto the embedded
	// Command, so Bin is non-empty for one and the Bin check alone would hand back a
	// key the doc above promises does not exist.
	if o.IsService() || o.Bin == "" {
		return nil
	}
	key := make([]string, 0, len(o.Args)+1)
	key = append(key, "bin:"+o.Bin)
	for _, a := range o.Args {
		key = append(key, "arg:"+a)
	}
	return key
}
