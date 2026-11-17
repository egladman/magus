// Package trail writes the magus activity trail: a durable, append-only record of consequential
// actions taken against the server (who did what, and did it succeed) kept next to the
// execution journal under a base directory. The event and its readers are
// libs/safetyscissors/trail, which exports no writer; this package is the writer magus's
// producers (the MCP handler, agent hooks, background jobs) call until the writer moves into
// the module with the guard. The console's ActivityService reads through the module.
//
// The magus.activity.v1alpha1 proto is the WIRE format only; the handler maps Event to it;
// this package never depends on the proto or the handler stack.
//
// Every operation is a stateless free function taking the trail's base directory. Writes are
// low-frequency (agent tool calls, background jobs), so a per-call open/append/close is cheap
// and avoids a long-lived handle, a lock, and a second write path. POSIX append of a short line
// is atomic, so concurrent producers do not interleave; the "lines stay small" invariant
// (events carry payload REFS, never bodies) is what keeps that true.
package trail

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	trailtypes "github.com/egladman/magus/libs/safetyscissors/trail"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/types"
)

// dir is the base-dir subdirectory holding the trail, a sibling of the journal's runs/.
const dir = "activity"

const (
	eventsFile  = "events.jsonl" // append-only JSONL, one Event per line
	blobsSubDir = "blobs"        // content-addressed request/response payloads
)

// refHexLen and validPrefix copy libs/safetyscissors/trail's, which keeps its own unexported
// until the writer moves into the module; the layout under activity/ must match it.
//
// refHexLen is how many hex chars of a payload's SHA-256 name its blob. 16 (64 bits) is ample
// against content collisions among one machine's payloads while staying short in a ref.
const refHexLen = 16

// maxEvents caps the trail: Rotate keeps the most recent maxEvents events and garbage-collects
// blobs no kept event references. Rotate runs at server start and thereafter on the server's
// rotate-activities schedule, never per append, because the trail is stateless and lock-free by
// design (append is a bare POSIX append; there is no long-lived handle to hang a count on), so a
// write-triggered rotate would have to re-scan the whole file per write.
//
// The overshoot between scheduled runs is therefore bounded by the SCHEDULE, not by a counter.
// That is the honest shape: a counter can only ever bound the one producer that owns it, and the
// producers that most need bounding (short-lived agent hooks) have nowhere to keep one.
const maxEvents = 10000

// AgentCommand is the normalized, host-independent observation an agent hook contributes to the
// trail. Command and Path are mutually exclusive in the supplied hooks: the former is a shell-tool
// invocation, the latter a file-edit invocation. Host integrations may omit identity fields when
// their hook event does not expose them; the event is then unattributed rather than pretending
// to know more than the host supplied.
// Transcript is the host's own record of the session this observation came from, as an
// absolute path on the machine that produced it. It is a POINTER, never content: the trail
// stays a record of paths and timings, and a reader who wants what was actually said opens
// the transcript themselves. That division is what lets the trail carry a whole session's
// reach cheaply while the expensive, sensitive detail stays where the host already put it.
//
// It travels beside Session rather than replacing it: the id is what groups the events, and
// the path is what a reader follows to see the rest. A host that exposes no transcript sends
// none, exactly as with the other identity fields.
//
// Lease is the lease the producer resolved for this observation through job.LeaseQuery,
// with LeaseFrom naming its source; empty when nothing answered.
type AgentCommand struct {
	Workspace string
	// EntryPoint is where the observation entered magus; empty records a hook.
	EntryPoint job.EntryPoint
	Host       string
	Session    string
	// Agent is the host's subagent id, empty for the main conversation.
	Agent      string
	Transcript string
	Event      string
	Tool       string
	Command    string
	Path       string
	Decision   verdict.Decision
	Reason     string
	Context    string
	// Rule is the catalogued rule behind a deny, advise or ask; empty for a defer.
	Rule      string
	Lease     string
	LeaseFrom job.LeaseSource
	// PreauthorizedBy is the `next` template that had already served this exact command to
	// this session, so the guard let it through without grading it against the caller's
	// role. Empty for every other observation, which is nearly all of them.
	PreauthorizedBy string
	// VerdictRef is the grd blob a repeat deny cites; see Event.VerdictRef.
	VerdictRef string
	// PolicyDigest and DecidedBy link the verdict to the rules that reached it; see Event.
	PolicyDigest string
	DecidedBy    string
	// RuleFailures are the workspace command rules that judged nothing, empty when every
	// rule answered.
	RuleFailures []RuleFailure
	// Binary, BinaryVersion and Cwd are the judging magus and the directory it judged in,
	// and Phases its timed steps; see Event.
	Binary        string
	BinaryVersion string
	Cwd           string
	Phases        []trailtypes.Phase
	// GuardMs is how long the guard took to judge the call. A pre-call
	// hook never sees the run time, so this is not tool time.
	GuardMs int64
	// ToolMs is the run time the host reports on its post-call hook. It
	// becomes the event's duration_ms. Zero when the host reports none.
	ToolMs int64
	// AgentFacts are the caller's cost facts, recorded in the request blob.
	types.AgentFacts
}

const agentCommandAPIVersion types.APIVersion = "magus.agent-command/v1alpha1"

type agentCommandRequest struct {
	APIVersion types.APIVersion `json:"api_version"`
	Host       string           `json:"host,omitempty"`
	Session    string           `json:"session,omitempty"`
	Transcript string           `json:"transcript,omitempty"`
	Event      string           `json:"event,omitempty"`
	Tool       string           `json:"tool,omitempty"`
	Command    string           `json:"command,omitempty"`
	Path       string           `json:"path,omitempty"`
	// Additive for the reason agentCommandResponse.PreauthorizedBy gives.
	types.AgentFacts
}

type agentCommandResponse struct {
	APIVersion types.APIVersion `json:"api_version"`
	Decision   verdict.Decision `json:"decision"`
	Reason     string           `json:"reason,omitempty"`
	Context    string           `json:"context,omitempty"`
	Rule       string           `json:"rule,omitempty"`
	// No schema bump: an added optional field a reader can ignore leaves every existing
	// blob readable and every existing reader correct.
	PreauthorizedBy string        `json:"preauthorized_by,omitempty"`
	RuleFailures    []RuleFailure `json:"rule_failures,omitempty"`
}

// retiredDecisionError is an agent-command blob holding "pass" or "allow", the names the
// guard's defer had before its decisions became defer, advise, ask and deny. A reader
// refuses the store rather than drop the observation from what it counts.
type retiredDecisionError struct {
	// Store is the activity directory the blob was read from, and Ref the blob.
	Store    string
	Ref      string
	Decision string
}

func (e *retiredDecisionError) Error() string {
	return fmt.Sprintf("activity: %s holds the retired guard decision %q in blob %s: clear that store (remove the directory) or start a fresh session", e.Store, e.Decision, e.Ref)
}

// blobUndecodableError is an agent-command blob that is there and does not decode. A
// reader returns it beside what the other blobs held, rather than count around it.
type blobUndecodableError struct {
	// Store is the activity directory the blob was read from, and Ref the blob.
	Store string
	Ref   string
	Err   error
}

func (e *blobUndecodableError) Error() string {
	return fmt.Sprintf("activity: blob %s in %s does not decode, so what it recorded is not counted: %v", e.Ref, e.Store, e.Err)
}

func (e *blobUndecodableError) Unwrap() error { return e.Err }

// decodeRequestBlob decodes the request blob ref under base into req. A retired decision
// is a [*retiredDecisionError] and any other failure a [*blobUndecodableError].
func decodeRequestBlob(base, ref string, raw []byte, req *agentCommandRequest) error {
	err := decodeAgentBlob(base, ref, raw, req)
	var retired *retiredDecisionError
	if err == nil || errors.As(err, &retired) {
		return err
	}
	return &blobUndecodableError{Store: filepath.Join(base, dir), Ref: ref, Err: err}
}

// retiredDecisions are the decision names no build after the defer rename writes.
func retiredDecisions() []string { return []string{"pass", "allow"} }

// decodeAgentBlob decodes the agent-command blob ref under base into v. A retired decision
// is a [*retiredDecisionError]; any other failure is returned as is.
func decodeAgentBlob(base, ref string, raw []byte, v any) error {
	err := codec.Unmarshal(raw, v)
	var unknown *verdict.UnknownDecisionError
	if errors.As(err, &unknown) && slices.Contains(retiredDecisions(), unknown.Decision) {
		return &retiredDecisionError{Store: filepath.Join(base, dir), Ref: ref, Decision: unknown.Decision}
	}
	return err
}

// redactFailures redacts each failure's error text, which quotes workspace code and may
// quote a command line.
func redactFailures(ctx context.Context, in []RuleFailure) []RuleFailure {
	if len(in) == 0 {
		return nil
	}
	out := make([]RuleFailure, len(in))
	for i, f := range in {
		out[i] = RuleFailure{Side: f.Side, Error: secrets.RedactString(ctx, f.Error)}
	}
	return out
}

// AppendAgentCommand writes one normalized agent-hook observation into the existing activity
// trail. Its blobs deliberately hold only the command-audit contract, not the raw host event: host
// events often contain unrelated model and editor fields, while the normalized payload has the
// stable provenance needed to answer who requested which tool action and how the guard replied.
//
// The Outcome is OK when this best-effort observation was constructed; it does not claim that the
// requested command ran or succeeded. A pre-execution hook has no such knowledge. Callers must not
// make command execution contingent on the trail, so this function follows Append's best-effort,
// error-free contract.
func AppendAgentCommand(ctx context.Context, base string, command AgentCommand) {
	// A tool-time row names its tool and carries no command or path.
	if base == "" || (command.Command == "" && command.Path == "" && command.ToolMs == 0) {
		return
	}
	// A hook observes a shell command line, which is exactly where a credential passed as an
	// argument shows up. Redacted here rather than in the blob alone, because these fields also
	// reach Action and Preview on the event line.
	command.Command = secrets.RedactString(ctx, command.Command)
	command.Path = secrets.RedactString(ctx, command.Path)
	command.Reason = secrets.RedactString(ctx, command.Reason)
	command.Context = secrets.RedactString(ctx, command.Context)
	command.ContextModel = clampRunes(command.ContextModel, MaxSpawnerLen)
	request, _ := codec.Marshal(agentCommandRequest{
		APIVersion: agentCommandAPIVersion,
		Host:       command.Host,
		Session:    command.Session,
		Transcript: command.Transcript,
		Event:      command.Event,
		Tool:       command.Tool,
		Command:    command.Command,
		Path:       command.Path,
		AgentFacts: command.AgentFacts,
	})
	response, _ := codec.Marshal(agentCommandResponse{
		APIVersion:      agentCommandAPIVersion,
		Decision:        command.Decision,
		Reason:          command.Reason,
		Context:         command.Context,
		Rule:            command.Rule,
		PreauthorizedBy: command.PreauthorizedBy,
		RuleFailures:    redactFailures(ctx, command.RuleFailures),
	})
	reqRef, reqBytes := WriteBlob(ctx, base, "agent", request)
	respRef, respBytes := WriteBlob(ctx, base, "agent", response)

	// The lease is the one the producer resolved through job.LeaseQuery, recorded as given:
	// reading BAGGAGE here as well would be a second resolution order. One that fails
	// job.ValidID is dropped with its source, since no join beats a wrong one.
	//
	// The prompt-marker contract is deliberately NOT run here. An observation carries a command
	// line and a guard's reason, not a lease prompt, and a "lease:" line inside either is
	// quoted prose rather than an orchestrator's assertion.
	lease, leaseFrom := command.Lease, command.LeaseFrom
	if !job.ValidID(lease) {
		lease, leaseFrom = "", ""
	}

	action := command.Tool
	if action == "" {
		action = "command"
	}
	preview := "observed"
	if command.Decision != "" {
		preview = "guard: " + string(command.Decision)
	}
	Append(ctx, base, trailtypes.Event{
		Ts:            time.Now().UnixMilli(),
		Kind:          trailtypes.KindAgentCommand,
		Origin:        hookOrigin(job.Origin{EntryPoint: command.EntryPoint, Host: command.Host, Session: command.Session, Agent: command.Agent}),
		Workspace:     command.Workspace,
		Action:        action,
		Lease:         lease,
		LeaseFrom:     leaseFrom,
		Outcome:       trailtypes.OutcomeOK,
		DurationMs:    command.ToolMs,
		GuardMs:       command.GuardMs,
		RequestRef:    reqRef,
		RequestBytes:  reqBytes,
		ResponseRef:   respRef,
		ResponseBytes: respBytes,
		VerdictRef:    command.VerdictRef,
		Preview:       preview,
		PolicyDigest:  command.PolicyDigest,
		DecidedBy:     command.DecidedBy,
		Binary:        command.Binary,
		BinaryVersion: command.BinaryVersion,
		Cwd:           command.Cwd,
		Phases:        command.Phases,
	})
}

// AgentSpawn is the normalized, host-independent observation that an orchestrating agent handed
// work to a subagent. Child is whatever label the host's event supplied for the callee (a
// subagent type, a task description, the spawning tool's name); Context is the text actually
// handed over, which is the whole point of the record and the reason it goes to a blob.
//
// There is no Decision field, unlike AgentCommand: a spawn is not judged by the guard. The handed
// context is prose, not a command line, and judging it as one would deny a lease for quoting
// a denied command in its instructions.
//
// DeclaredModel is what the spawning tool_input claimed about which model the child runs as,
// or "" when the caller named none; the same trust tier as [BaggageSpawner]: the spawning
// process's own assertion about itself, recorded verbatim and corroborated by nothing. NO
// verdict may key on it; a spawn stays outside the guard whatever it claims. Named
// DeclaredModel rather than Model because "model" already names the guard's reply CHANNEL in
// its coverage vocabulary (deny=model, advise=model, defer=none), and a bare Model field here
// would read as one more of those instead of a spawn's own claim.
type AgentSpawn struct {
	Workspace string
	// EntryPoint is where the observation entered magus; empty records a hook.
	EntryPoint job.EntryPoint
	Host       string
	Session    string
	// Agent is the host's id for the subagent that spawned, empty for the main
	// conversation.
	Agent         string
	Event         string
	Tool          string
	Child         string
	Context       string
	DeclaredModel string
	// VerdictRef is the grd blob a deny or advisory cites; see Event.VerdictRef.
	VerdictRef string
	// PolicyDigest and DecidedBy link the spawn's verdict to the rules that reached it;
	// see Event.
	PolicyDigest string
	DecidedBy    string
	// Continue marks a message to a subagent that already exists rather than a new one.
	Continue bool
	// Target is the agent a continuation addresses, "" for a spawn.
	Target string
	// RuleFailures are the workspace spawn rules that judged nothing, empty when every
	// rule answered.
	RuleFailures []RuleFailure
	// Binary, BinaryVersion and Cwd are the judging magus and the directory it judged in,
	// and Phases its timed steps; see Event.
	Binary        string
	BinaryVersion string
	Cwd           string
	Phases        []trailtypes.Phase
	// AgentFacts are the CALLER's, as on AgentCommand. Its ContextModel is the model the
	// caller runs as, never the child's: that one is DeclaredModel.
	types.AgentFacts
}

// RuleFailure is one side of a workspace guard rule that judged nothing, and why.
type RuleFailure struct {
	// Side is worktree or approved, the values Event.DecidedBy names them by.
	Side  string `json:"side"`
	Error string `json:"error"`
}

const agentSpawnAPIVersion types.APIVersion = "magus.agent-spawn/v1alpha1"

type agentSpawnRequest struct {
	APIVersion types.APIVersion `json:"api_version"`
	Host       string           `json:"host,omitempty"`
	Session    string           `json:"session,omitempty"`
	Event      string           `json:"event,omitempty"`
	Tool       string           `json:"tool,omitempty"`
	Child      string           `json:"child,omitempty"`
	Lease      string           `json:"lease,omitempty"`
	Context    string           `json:"context"`
	// DeclaredModel is additive: an older reader ignores a field it does not know, and one
	// reading a record written before this field existed gets "" for it, which is exactly
	// "no model declared": the same fact a genuinely undeclared spawn reports. No schema
	// bump, for the reason agentCommandResponse.PreauthorizedBy already documents.
	DeclaredModel string `json:"declared_model,omitempty"`
	// Target and RuleFailures are additive for the reason DeclaredModel is.
	Target       string        `json:"target,omitempty"`
	RuleFailures []RuleFailure `json:"rule_failures,omitempty"`
	types.AgentFacts
}

const (
	// ActionAgentSpawn is the action of an agent_spawn event whose host named no child.
	ActionAgentSpawn = "agent.spawn"
	// ActionAgentContinue is the action of an agent_spawn event recording a message to a
	// subagent that already exists.
	ActionAgentContinue = "agent.continue"
)

// AppendAgentSpawn records one spawn or continuation and stores the context it was given
// as a blob.
//
// Best-effort and error-free, like every other producer here: an audit write must never be able
// to fail the lease it observes.
//
// NOTE ON GROWTH: this producer runs in the short-lived hook process, which has no append counter
// to drive RotateOnCount, so nothing it writes triggers a rotate; only the server's boot-time
// Rotate bounds the trail. That was already true of AppendAgentCommand; it bites harder here
// because a spawn blob is a whole lease prompt rather than one command line.
func AppendAgentSpawn(ctx context.Context, base string, spawn AgentSpawn) {
	if base == "" || spawn.Context == "" {
		return
	}
	// A lease prompt is free text an agent composed, and an orchestrator that pastes a
	// connector token into a sub-agent's instructions is exactly the lease worth auditing
	// WITHOUT persisting the token. Redacted before the marker scan so a redaction can never
	// invent or destroy a lease id after the fact.
	spawn.Context = secrets.RedactString(ctx, spawn.Context)
	spawn.Child = secrets.RedactString(ctx, spawn.Child)
	// Clamped like the magus.spawner claim it sits beside (see MaxSpawnerLen): a model
	// name is short, and an unbounded claim is a cost every later read of the
	// repository would pay for one bad payload.
	spawn.DeclaredModel = clampRunes(spawn.DeclaredModel, MaxSpawnerLen)
	spawn.ContextModel = clampRunes(spawn.ContextModel, MaxSpawnerLen)
	lease := leaseFromContext(spawn.Context)
	failures := redactFailures(ctx, spawn.RuleFailures)
	request, _ := codec.Marshal(agentSpawnRequest{
		APIVersion:    agentSpawnAPIVersion,
		Host:          spawn.Host,
		Session:       spawn.Session,
		Event:         spawn.Event,
		Tool:          spawn.Tool,
		Child:         spawn.Child,
		Lease:         lease,
		Context:       spawn.Context,
		DeclaredModel: spawn.DeclaredModel,
		Target:        spawn.Target,
		RuleFailures:  failures,
		AgentFacts:    spawn.AgentFacts,
	})
	reqRef, reqBytes := WriteBlob(ctx, base, "spawn", request)

	// The CHILD is the action, the way an MCP call's action is its tool name: it is the field a
	// reader groups a page of leases by. A host that supplied no label leaves the generic
	// verb, so the row still says what happened. A continuation starts no child, so it is
	// always its own verb and a spawn count can leave it out.
	action := spawn.Child
	switch {
	case spawn.Continue:
		action = ActionAgentContinue
	case action == "":
		action = ActionAgentSpawn
	}
	Append(ctx, base, trailtypes.Event{
		Ts:            time.Now().UnixMilli(),
		Kind:          trailtypes.KindAgentSpawn,
		Origin:        hookOrigin(job.Origin{EntryPoint: spawn.EntryPoint, Host: spawn.Host, Session: spawn.Session, Agent: spawn.Agent}),
		Workspace:     spawn.Workspace,
		Action:        action,
		Lease:         lease,
		Outcome:       trailtypes.OutcomeOK,
		RequestRef:    reqRef,
		RequestBytes:  reqBytes,
		VerdictRef:    spawn.VerdictRef,
		PolicyDigest:  spawn.PolicyDigest,
		DecidedBy:     spawn.DecidedBy,
		Binary:        spawn.Binary,
		BinaryVersion: spawn.BinaryVersion,
		Cwd:           spawn.Cwd,
		Phases:        spawn.Phases,
	})
}

// LeaseFromEnv returns the lease this process is ACTING as, or "" when it claims
// none: the magus.lease member of the W3C BAGGAGE channel. See [SpawnFromEnv], which
// reads the rest of what that environment claimed.
//
// It is the worker's own claim about itself, unlike a prompt's lease line (see
// leaseFromContext), which is the orchestrator's assertion about a spawn it is making.
// Grading ranks it last, below every record; see job.LeaseQuery.Resolve.
func LeaseFromEnv() string { return SpawnFromEnv().Lease }

// leaseScanBytes bounds the head of the handed context the marker may appear in. The marker
// leads the prompt, so this is a cap on one pathological first line rather than a window to
// search: it keeps a multi-megabyte single-line payload from being scanned at all.
const leaseScanBytes = 4096

// leaseMarker is the prefix the marker line carries.
const leaseMarker = "lease:"

// leaseFromContext returns the lease a prompt declares, or "" when it declares
// none. THE MARKER CONTRACT, documented once here and in the host glue pages:
//
//	the FIRST non-blank line of the handed context, trimmed, reading exactly
//	"lease: <id>"
//
// First line, not anywhere in the head: a lease prompt routinely quotes things (a
// ledger listing, a file, another agent's transcript), and a marker line lifted from any
// of them would stamp the event with a lease that has nothing to do with this
// spawn. A marker an orchestrator wrote is at the top, and a marker in quoted prose is
// not; the position is the only thing that separates them. Leading blank lines are
// formatting and are skipped.
//
// The id itself has to satisfy [job.ValidID], which is where the charset and
// its reasoning live.
//
// Anything else (no marker, an empty id, an id carrying spaces or punctuation outside
// the set, a marker below the first line) yields "". Correlation is cooperative: a
// missing join is the designed outcome, never an error.
func leaseFromContext(handed string) string {
	head := handed
	if len(head) > leaseScanBytes {
		head = head[:leaseScanBytes]
	}
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		rest, found := strings.CutPrefix(line, leaseMarker)
		if !found {
			return "" // the prompt leads with something else, so it declares no lease
		}
		id := strings.TrimSpace(rest)
		if !job.ValidID(id) {
			return ""
		}
		return id
	}
	return ""
}

func eventsPath(base string) string { return filepath.Join(base, dir, eventsFile) }
func blobsPath(base string) string  { return filepath.Join(base, dir, blobsSubDir) }

// Append records one event under base. Best-effort: an empty base or any I/O error is dropped,
// because the trail is never a precondition for the action it records. Concurrent Appends from
// different producers do not interleave: each is a single POSIX append of one short line.
//
// ctx carries the run's secret resolver, and every free-text field goes through it first. The
// trail is DURABLE and append-only, so a credential that lands here is on disk until someone
// deletes the file, strictly worse than the same value reaching a terminal. It took a ctx
// parameter for no other reason.
func Append(ctx context.Context, base string, e trailtypes.Event) {
	if base == "" {
		return
	}
	e = redactEvent(ctx, e)
	e.Origin = StampOrigin(ctx, e.Origin)
	line, err := codec.Marshal(e)
	if err != nil {
		return
	}
	path := eventsPath(base)
	// Made first: the file helpers create a missing dir world-readable.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	_ = codec.AppendLines(path, [][]byte{line}, 0o600, codec.WithoutSync())
}

// WriteBlob stores a payload under a provenance prefix (e.g. "mcp") and returns its ref and byte
// size. An empty base, invalid prefix, empty data, or write failure yields an empty ref (the
// caller omits it) while still reporting the size. Idempotent by content and replaced whole,
// so a concurrent reader never observes a partial blob.
func WriteBlob(ctx context.Context, base, prefix string, data []byte) (ref string, size int64) {
	// Redacted BEFORE the ref is computed, so the content address names what is actually
	// stored. Blobs are the highest-risk data in this package: an MCP request/response pair
	// is a whole tool payload, persisted verbatim, and nothing else on the path scrubs it.
	data = secrets.Redact(ctx, data)
	size = int64(len(data))
	if base == "" || len(data) == 0 || !validPrefix(prefix) {
		return "", size
	}
	ref = blobRef(prefix, data)
	path := filepath.Join(blobsPath(base), ref)
	if _, err := os.Stat(path); err == nil {
		// A dedup hit is a fresh reference to an old file, and gcBlobs decides by mtime: without
		// this touch a rotate between here and the caller's Append sees a blob older than
		// blobGraceWindow that no kept event names yet, and deletes a payload about to go live.
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		return ref, size
	}
	// 0600 because a blob is a whole tool payload, which redaction scrubs of what it
	// recognizes and no more.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", size
	}
	if err := file.ReplaceFile(path, data, 0o600); err != nil {
		return "", size
	}
	return ref, size
}

// LastRun returns the most recent KIND_JOB event whose Action equals action (the space-joined
// worker argv recorded for a background job, e.g. "graph build") and whether one was found.
// It is how a caller shows a job's last outcome. Scans the retained trail newest-first; a job
// that has not run within it is (zero, false).
func LastRun(ctx context.Context, base, action string) (trailtypes.Event, bool) {
	events, _ := readRecent(ctx, base, maxEvents)
	for _, e := range events { // newest-first
		if e.Kind == trailtypes.KindJob && e.Action == action {
			return e, true
		}
	}
	return trailtypes.Event{}, false
}

// Stat reports the trail's current on-disk footprint under base: total bytes (the events file
// plus every payload blob) and the number of recorded events. It is what a caller shows to
// judge whether a rotate is worth running. Best-effort and read-only: a missing or empty trail
// is (0, 0), and an unreadable directory is skipped rather than erroring: a size readout is
// never a precondition for anything.
func Stat(base string) (size int64, count int64) {
	if base == "" {
		return 0, 0
	}
	if f, err := os.Open(eventsPath(base)); err == nil {
		if fi, err := f.Stat(); err == nil {
			size += fi.Size()
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			if len(sc.Bytes()) > 0 {
				count++
			}
		}
		f.Close()
	}
	if entries, err := os.ReadDir(blobsPath(base)); err == nil {
		for _, ent := range entries {
			if info, err := ent.Info(); err == nil && !ent.IsDir() {
				size += info.Size()
			}
		}
	}
	return size, count
}

// Rotate keeps the last maxEvents events and deletes blobs that no kept event references.
// Best-effort: any error leaves the trail as-is, and it only rewrites when the file exceeds the
// cap. It takes no lock, so a concurrent Append racing the read-rewrite window can be dropped:
// acceptable for a best-effort governance trail, and the price of keeping the trail lock-free.
//
// It is CHEAP to call on a trail that is already small (see minEventBytes), which is what lets the
// server's maintenance schedule be the single owner of rotation. A second, write-triggered path
// (a rotate driven off a producer's own append counter) can only cover that one producer: an agent
// hook is a short-lived process with nowhere to keep a counter, so a hook-fed trail would be
// write-bounded by nothing at all. One trigger that every producer shares beats two that disagree
// about who is covered.
func Rotate(base string) { rotate(base, maxEvents) }

// minEventBytes is a floor on one serialized event line, used to skip the read entirely when the
// file is too small to hold maxEvents of them. It is a SOUND bound rather than a guess: Ts, Kind,
// Action and Outcome have no omitempty, so even an all-empty event marshals to 43 bytes plus a
// newline. Rounding down to 40 keeps the check conservative: it can only ever decide to look when
// it did not need to, never to skip when it did.
const minEventBytes = 40

// perKindFloor is how many of a kind's newest events survive a rotate regardless of how loud
// its neighbors are.
//
// Plain recency is the wrong policy for a record with kinds this uneven. One chatty producer
// (an agent hook wired to a read tool is the obvious one, but MCP tool calls do it too) can push
// every sandbox_denial and config_change out of the window and, because gcBlobs then unlinks
// whatever no kept line references, DELETE their payloads. The rare kinds are exactly the ones
// worth keeping: nobody consults this file to find out that a read happened.
//
// A floor rather than an equal split, because an equal split wastes the window on kinds that are
// idle. With the kinds defined here this reserves a minority of maxEvents and leaves the rest to
// straight recency, so a quiet trail behaves exactly as it did before.
const perKindFloor = 500

// selectKept chooses which lines survive a rotate: every kind keeps its newest perKindFloor
// events, and whatever budget remains goes to the newest events of any kind. Order is preserved,
// because the file is append-ordered and every reader (ReadRecent, LastRun) depends on that.
//
// Kind is read with a narrow decode rather than a full Event unmarshal: this runs over the whole
// file, and the only field the policy needs is the kind. A line that fails to decode has no kind
// to reserve against and competes on recency alone, which is the same treatment ReadRecent gives
// a corrupt line.
func selectKept(lines []string, max int) []string {
	type lineKind struct {
		Kind string `json:"kind"`
	}
	kindOf := make([]string, len(lines))
	present := map[string]bool{}
	for i, l := range lines {
		if l == "" {
			continue
		}
		var lk lineKind
		if err := codec.Unmarshal([]byte(l), &lk); err != nil {
			continue
		}
		kindOf[i] = lk.Kind
		if lk.Kind != "" {
			present[lk.Kind] = true
		}
	}

	// The reservation is the floor OR an equal share of the window, whichever is smaller.
	// Without the share, a floor larger than the window lets the first pass spend the whole
	// budget on whichever kind happens to be newest, which is precisely the eviction this
	// policy exists to prevent, reintroduced by the policy itself.
	reserve := perKindFloor
	if len(present) > 0 && max/len(present) < reserve {
		reserve = max / len(present)
	}

	admit := make([]bool, len(lines))
	seen := map[string]int{}
	budget := max

	// Newest first, so "the newest N of this kind" falls out of the iteration order.
	for i := len(lines) - 1; i >= 0 && budget > 0; i-- {
		if kindOf[i] == "" || seen[kindOf[i]] >= reserve {
			continue
		}
		seen[kindOf[i]]++
		admit[i] = true
		budget--
	}
	// Second pass fills the remaining budget purely by recency, which is what keeps a
	// single-kind trail behaving exactly as plain truncation did.
	for i := len(lines) - 1; i >= 0 && budget > 0; i-- {
		if lines[i] == "" || admit[i] {
			continue
		}
		admit[i] = true
		budget--
	}

	kept := make([]string, 0, max)
	for i, ok := range admit {
		if ok {
			kept = append(kept, lines[i])
		}
	}
	return kept
}

func rotate(base string, max int) {
	if base == "" {
		return
	}
	path := eventsPath(base)
	// A stat before the read is what makes a frequent schedule affordable. Without it, every
	// check reads the whole trail: fine at a 30-day cadence, wasteful at an hourly one, and
	// worst exactly when the file has grown large enough to matter.
	if info, err := os.Stat(path); err == nil && info.Size() < int64(max)*minEventBytes {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= max {
		return
	}
	kept := selectKept(lines, max)

	var b strings.Builder
	for _, l := range kept {
		if l != "" {
			b.WriteString(l + "\n")
		}
	}
	// Synced, unlike a blob: a crash that emptied this file would lose the whole trail,
	// where an unsynced blob loses one payload.
	if err := file.WriteFileAtomic(path, []byte(b.String()), 0o600); err != nil {
		return
	}
	gcBlobs(base, kept)
}

// blobGraceWindow holds back GC on a blob younger than this, even when no kept event
// references it yet. WriteBlob finalizes (renames) a blob before its producer's Append
// writes the event that references it; a rotate landing in that gap would otherwise see
// the blob as unreferenced by the snapshot it just read and delete it out from under the
// still-pending event: the event survives, the payload is gone. Every producer in this
// tree calls WriteBlob immediately followed by Append on the same goroutine (wrap() in
// internal/handler/mcp, AppendAgentCommand above), so the real gap is microseconds; this
// window is deliberately generous to also absorb scheduler preemption, without adding a
// lock that would serialize Append behind rotation.
const blobGraceWindow = 30 * time.Second

// gcBlobs removes blob files that none of the kept event lines reference AND that are
// older than blobGraceWindow. Temp files (from an in-flight WriteBlob) fail trailtypes.ValidRef and
// are left alone regardless of age.
func gcBlobs(base string, keptLines []string) {
	referenced := make(map[string]struct{})
	for _, l := range keptLines {
		var e trailtypes.Event
		if codec.Unmarshal([]byte(l), &e) != nil {
			continue
		}
		if e.RequestRef != "" {
			referenced[e.RequestRef] = struct{}{}
		}
		if e.ResponseRef != "" {
			referenced[e.ResponseRef] = struct{}{}
		}
		if e.VerdictRef != "" {
			referenced[e.VerdictRef] = struct{}{}
		}
	}
	entries, err := os.ReadDir(blobsPath(base))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-blobGraceWindow)
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !trailtypes.ValidRef(name) {
			continue
		}
		if _, ok := referenced[name]; ok {
			continue
		}
		info, err := ent.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue // too young: its event may not have landed yet
		}
		_ = os.Remove(filepath.Join(blobsPath(base), name))
	}
}

// blobRef is the content-addressed name for a payload: its provenance prefix plus the first
// refHexLen hex chars of its SHA-256.
func blobRef(prefix string, data []byte) string {
	sum := sha256.Sum256(data)
	return prefix + hex.EncodeToString(sum[:])[:refHexLen]
}

// validPrefix accepts 2 to 8 lowercase letters, a short provenance tag like "mcp".
func validPrefix(prefix string) bool {
	if len(prefix) < 2 || len(prefix) > 8 {
		return false
	}
	for i := 0; i < len(prefix); i++ {
		if prefix[i] < 'a' || prefix[i] > 'z' {
			return false
		}
	}
	return true
}

// redactEvent masks every free-text field on an event.
//
// The structural fields are deliberately left alone: Kind, Outcome, the Origin, Workspace,
// Lease and the blob refs are enumerated values, identities and content addresses, none
// of which a credential can occupy, and all of which a reader filters on by exact match. Redacting
// them would break the activity view to protect nothing, the same reasoning that leaves slog
// attribute KEYS alone in libs/secrets. Lease is the one of those derived from free text (a
// lease prompt, or the BAGGAGE environment channel) rather than supplied by a caller,
// which is why every channel that can stamp one runs it through [job.ValidID]'s bare-identifier
// rule before it can reach this exemption.
func redactEvent(ctx context.Context, e trailtypes.Event) trailtypes.Event {
	e.Action = secrets.RedactString(ctx, e.Action)
	e.Error = secrets.RedactString(ctx, e.Error)
	e.Preview = secrets.RedactString(ctx, e.Preview)
	return e
}

type baseKey struct{}

// ContextWithBase carries the trail's base directory so a producer deep in the run (a
// magusfile binding, say) can record an event without being handed the path.
//
// Every other producer receives its base as an argument, which is the better shape when
// the call site is a handler constructed with it. A magusfile binding is not: it runs
// inside the Buzz VM, several layers below anything that knows a cache directory, and
// threading a string through those layers to reach one Append would put the trail into
// signatures that have no other reason to mention it.
func ContextWithBase(ctx context.Context, base string) context.Context {
	if base == "" {
		return ctx
	}
	return context.WithValue(ctx, baseKey{}, base)
}

// BaseFromContext returns the trail base, or "" when none was installed. An empty base
// makes every write a no-op, which is the package's existing best-effort contract.
func BaseFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	base, _ := ctx.Value(baseKey{}).(string)
	return base
}
