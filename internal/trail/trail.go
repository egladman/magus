// Package trail is the magus activity trail: a durable, append-only record of consequential
// actions taken against the daemon - who did what, and did it succeed - kept next to the
// execution journal under a base directory. It is the store behind the magus.activity.v1alpha1
// "activity view"; producers (the MCP handler, agent hooks, and background jobs today; config
// and token lifecycle later) append events and store payload blobs, and the console's
// ActivityService reads them for the log viewer.
//
// It is the governance sibling of internal/journal (which records what a build executed) and
// mirrors its Event/JSONL shape: a hand-rolled Event struct, snake_case json, one json.Marshal
// line per event, plus a content-addressed blob store for large payloads. The magus.activity.v1alpha1
// proto is the WIRE format only; the handler maps Event to it - this package never depends on
// the proto or the handler stack.
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
	"strings"
	"time"

	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/secret"
	"github.com/egladman/magus/types"
)

// dir is the base-dir subdirectory holding the trail, a sibling of the journal's runs/.
const dir = "activity"

const (
	eventsFile  = "events.jsonl" // append-only JSONL, one Event per line
	blobsSubDir = "blobs"        // content-addressed request/response payloads
)

// refHexLen is how many hex chars of a payload's SHA-256 name its blob. 16 (64 bits) is ample
// against content collisions among one machine's payloads while staying short in a ref.
const refHexLen = 16

// maxEvents caps the trail: Rotate keeps the most recent maxEvents events and garbage-collects
// blobs no kept event references. Rotate runs at daemon start and thereafter on the daemon's
// rotate-activities schedule - never per append, because the trail is stateless and lock-free by
// design (append is a bare POSIX append; there is no long-lived handle to hang a count on), so a
// write-triggered rotate would have to re-scan the whole file per write.
//
// The overshoot between scheduled runs is therefore bounded by the SCHEDULE, not by a counter.
// That is the honest shape: a counter can only ever bound the one producer that owns it, and the
// producers that most need bounding - short-lived agent hooks - have nowhere to keep one.
const maxEvents = 10000

// Kind names an action's source; the values map to the magus.activity.v1alpha1 Kind enum at the wire.
// Readable strings on disk, like the journal's status strings. It is a NAMED string (not a bare string)
// so a producer or the audit interceptor cannot pass an arbitrary label where a Kind is wanted - the
// param is type-checked against these consts. MCP tool calls, agent hooks, and jobs have producers today;
// the rest name the governance sources the envelope is built to hold, so a reader can switch on kind and a
// producer adds one without a schema change.
type Kind string

const (
	KindMCPToolCall    Kind = "mcp_tool_call"
	KindJob            Kind = "job"             // a daemon background job (SCIP reindex, graph build, VCS refresh)
	KindConfigChange   Kind = "config_change"   // magus.yaml changed on reload, or a config-set mutation
	KindTokenLifecycle Kind = "token_lifecycle" // a connector token minted or revoked
	KindSandboxDenial  Kind = "sandbox_denial"  // magus's own read/write/exec policy check refused an access (see sandbox.Policy); a kernel-landlock denial reports nothing back and cannot appear here
	// KindAgentCommand records an agent-host tool invocation observed by a configured hook. It is
	// an observation, not a process result: a PreToolUse hook runs before the host executes the
	// command, so its payload records the requested command or path and the guard's decision, never
	// an invented exit status. MCP calls remain KindMCPToolCall because their wrapper sees completion.
	KindAgentCommand Kind = "agent_command"
	// KindAgentSpawn records that an orchestrating agent handed work to a sub-agent, and WHAT
	// CONTEXT it handed over. It is the lease sibling of KindAgentCommand: same producer (a
	// pre-tool hook), same "observed, not executed" contract, but the thing observed is a context
	// transfer rather than a command, so there is no verdict to record and the guard never judges
	// one. The handed context lands in the request blob and only its REF rides the event, because
	// a lease prompt is routinely kilobytes.
	//
	// Correlation to a lease is COOPERATIVE, not enforced. Nothing in the host event
	// names a magus lease, and magus cannot infer one from prose, so the event's Lease is stamped
	// only when the handed context carries the documented marker (see leaseFromContext): its
	// FIRST non-blank line reading "lease: <id>". An orchestrator that wants the join writes the
	// marker; one that does not gets an event with an empty Lease, which is a missing join rather
	// than a wrong one - and a "lease:" line quoted deeper in a prompt stamps nothing, because a
	// wrong join is worse than none.
	KindAgentSpawn Kind = "agent_spawn"
	// KindMemory is the console MemoryService door onto the durable magus_memory files. Unlike the
	// other kinds it audits READS too (List/Get), not just edits: the memory files are the agent's
	// own handoff journal, so knowing when the operator inspected it is part of the governance story,
	// and the mount opts into read auditing (the agent/MCP door is already audited separately).
	KindMemory Kind = "memory"
	// KindNotes is the console NotesService door onto the workspace's human-authored notes.
	// Every event under it is a READ, because that service has no write path: a note's whole
	// value is the guarantee that a person wrote it, so the browser never becomes an author.
	// Reads are audited for one reason the shared store does not supply on its own - this is
	// the only surface that can serve the PRIVATE store, which is not in any repository and
	// which nothing else attributes.
	KindNotes Kind = "notes"

	// KindCredentialGrant records that a run made a credential reachable: a magusfile
	// granted one to a destination host, or opened a loopback endpoint carrying one.
	//
	// It is the governance half of a fact the execution journal already records. The
	// journal answers "what did this build do" and is queried per invocation; the trail
	// answers "who did what against this daemon", so when an AGENT triggers a run that
	// makes a credential spendable, this is the event that connects the two. Without it
	// the activity log shows the tool call and not its consequence.
	//
	// It carries the REFERENCE, the host and the header - never the value, which is not
	// resolved at declaration time and must not be resolved to log it.
	KindCredentialGrant Kind = "credential_grant" //nolint:gosec // G101: an event-kind discriminator whose name contains "credential", not a credential
)

// Outcome values; map to the wire Outcome enum.
const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
)

// Event is one recorded action, the on-disk atom of the trail. The envelope (Ts/Kind/Actor/
// Action/Outcome) is common to every kind; the payload refs point into the blob store so a large
// body never bloats the line. Field names are snake_case and match the journal's Event where
// they overlap (Ts, DurMs).
//
// Host and Session duplicate what an agent-hook event also records in its request blob, and that
// duplication is deliberate: a reader grouping a page of 200 rows by agent host must not have to
// fetch 200 blobs to do it. They stay short, so the "lines stay small" invariant holds.
type Event struct {
	Ts        int64  `json:"ts"`                   // unix milliseconds at the action's start
	Kind      Kind   `json:"kind"`                 // one of the Kind* constants
	Actor     string `json:"actor"`                // who: an agent id, "daemon", a user
	UserAgent string `json:"user_agent,omitempty"` // caller's HTTP User-Agent, when known (MCP over HTTP)
	Host      string `json:"host,omitempty"`       // the agent host that produced the action, as its own wrapper named itself; "" when the producer could not know
	Session   string `json:"session,omitempty"`    // the host's own session id, when its event carried one
	Workspace string `json:"workspace,omitempty"`  // repo-relative or absolute root the action pertained to; "" for daemon-wide (an MCP call is not bound to one workspace)
	Action    string `json:"action"`               // the specific action: a tool name, a job command, "connector.create"
	// Lease is the lease this action belongs to, when the producer could
	// correlate one (a marker line, or the BAGGAGE channel); ""
	// when uncorrelated.
	Lease         string `json:"lease,omitempty"`
	Outcome       string `json:"outcome"`                 // one of the Outcome* constants
	Error         string `json:"error,omitempty"`         // error text when Outcome is OutcomeError
	DurMs         int64  `json:"dur_ms,omitempty"`        // wall-clock, on call-shaped actions
	RequestRef    string `json:"request_ref,omitempty"`   // blob ref for the request body (mcp<hash>)
	ResponseRef   string `json:"response_ref,omitempty"`  // blob ref for the response body
	Preview       string `json:"preview,omitempty"`       // opening characters of the response, for list views
	RequestBytes  int64  `json:"request_bytes,omitempty"` // full request length
	ResponseBytes int64  `json:"response_bytes,omitempty"`
}

// AgentCommand is the normalized, host-independent observation an agent hook contributes to the
// trail. Command and Path are mutually exclusive in the supplied hooks: the former is a shell-tool
// invocation, the latter a file-edit invocation. Host integrations may omit identity fields when
// their hook event does not expose them; the event remains attributable to the generic "agent"
// actor rather than pretending to know more than the host supplied.
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
// Lease is the lease a producer already knew this observation belongs to, and is
// usually empty: a hook observes a command, not a lease, so nothing in the host event names
// one and [AppendAgentCommand] falls back to the environment channel.
type AgentCommand struct {
	Actor      string
	Workspace  string
	Host       string
	Session    string
	Transcript string
	Event      string
	Tool       string
	Command    string
	Path       string
	Decision   string
	Reason     string
	Context    string
	Lease      string
}

const agentCommandSchemaVersion = 1

type agentCommandRequest struct {
	SchemaVersion int    `json:"schema_version"`
	Host          string `json:"host,omitempty"`
	Session       string `json:"session,omitempty"`
	Transcript    string `json:"transcript,omitempty"`
	Event         string `json:"event,omitempty"`
	Tool          string `json:"tool,omitempty"`
	Command       string `json:"command,omitempty"`
	Path          string `json:"path,omitempty"`
}

type agentCommandResponse struct {
	SchemaVersion int    `json:"schema_version"`
	Decision      string `json:"decision"`
	Reason        string `json:"reason,omitempty"`
	Context       string `json:"context,omitempty"`
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
	if base == "" || (command.Command == "" && command.Path == "") {
		return
	}
	// A hook observes a shell command line, which is exactly where a credential passed as an
	// argument shows up. Redacted here rather than in the blob alone, because these fields also
	// reach Action and Preview on the event line.
	command.Command = secret.RedactString(ctx, command.Command)
	command.Path = secret.RedactString(ctx, command.Path)
	command.Reason = secret.RedactString(ctx, command.Reason)
	command.Context = secret.RedactString(ctx, command.Context)
	request, _ := json.Marshal(agentCommandRequest{
		SchemaVersion: agentCommandSchemaVersion,
		Host:          command.Host,
		Session:       command.Session,
		Transcript:    command.Transcript,
		Event:         command.Event,
		Tool:          command.Tool,
		Command:       command.Command,
		Path:          command.Path,
	})
	response, _ := json.Marshal(agentCommandResponse{
		SchemaVersion: agentCommandSchemaVersion,
		Decision:      command.Decision,
		Reason:        command.Reason,
		Context:       command.Context,
	})
	reqRef, reqBytes := WriteBlob(ctx, base, "agent", request)
	respRef, respBytes := WriteBlob(ctx, base, "agent", response)

	// A supplied lease is what the producer could correlate at the observation itself and wins;
	// the BAGGAGE channel is this process's own claim about itself and fills the gap. A supplied one that
	// fails types.ValidLeaseID falls through to the environment rather than being stamped, on the same
	// reasoning as everywhere else: no join beats a wrong one.
	//
	// The prompt-marker contract is deliberately NOT run here. An observation carries a command
	// line and a guard's reason, not a lease prompt, and a "lease:" line inside either is
	// quoted prose rather than an orchestrator's assertion.
	lease := command.Lease
	if !types.ValidLeaseID(lease) {
		lease = LeaseFromEnv()
	}

	action := command.Tool
	if action == "" {
		action = "command"
	}
	actor := command.Actor
	if actor == "" {
		actor = "agent"
	}
	preview := "observed"
	if command.Decision != "" {
		preview = "guard: " + command.Decision
	}
	Append(ctx, base, Event{
		Ts:            time.Now().UnixMilli(),
		Kind:          KindAgentCommand,
		Actor:         actor,
		Host:          command.Host,
		Session:       command.Session,
		Workspace:     command.Workspace,
		Action:        action,
		Lease:         lease,
		Outcome:       OutcomeOK,
		RequestRef:    reqRef,
		RequestBytes:  reqBytes,
		ResponseRef:   respRef,
		ResponseBytes: respBytes,
		Preview:       preview,
	})
}

// AgentSpawn is the normalized, host-independent observation that an orchestrating agent handed
// work to a sub-agent. Child is whatever label the host's event supplied for the callee (a
// sub-agent type, a task description, the spawning tool's name); Context is the text actually
// handed over, which is the whole point of the record and the reason it goes to a blob.
//
// There is no Decision field, unlike AgentCommand: a spawn is not a guard surface. The handed
// context is prose, not a command line, and judging it as one would deny a lease for quoting
// a denied command in its instructions.
type AgentSpawn struct {
	Actor     string
	Workspace string
	Host      string
	Session   string
	Event     string
	Tool      string
	Child     string
	Context   string
}

const agentSpawnSchemaVersion = 1

type agentSpawnRequest struct {
	SchemaVersion int    `json:"schema_version"`
	Host          string `json:"host,omitempty"`
	Session       string `json:"session,omitempty"`
	Event         string `json:"event,omitempty"`
	Tool          string `json:"tool,omitempty"`
	Child         string `json:"child,omitempty"`
	Lease         string `json:"lease,omitempty"`
	Context       string `json:"context"`
}

// AppendAgentSpawn records one lease handoff and stores the handed context as a blob.
//
// Best-effort and error-free, like every other producer here: an audit write must never be able
// to fail the lease it observes.
//
// NOTE ON GROWTH: this producer runs in the short-lived hook process, which has no append counter
// to drive RotateOnCount, so nothing it writes triggers a rotate - only the daemon's boot-time
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
	spawn.Context = secret.RedactString(ctx, spawn.Context)
	spawn.Child = secret.RedactString(ctx, spawn.Child)
	lease := leaseFromContext(spawn.Context)
	request, _ := json.Marshal(agentSpawnRequest{
		SchemaVersion: agentSpawnSchemaVersion,
		Host:          spawn.Host,
		Session:       spawn.Session,
		Event:         spawn.Event,
		Tool:          spawn.Tool,
		Child:         spawn.Child,
		Lease:         lease,
		Context:       spawn.Context,
	})
	reqRef, reqBytes := WriteBlob(ctx, base, "spawn", request)

	// The CHILD is the action, the way an MCP call's action is its tool name: it is the field a
	// reader groups a page of leases by. A host that supplied no label leaves the generic
	// verb, so the row still says what happened.
	action := spawn.Child
	if action == "" {
		action = "agent.spawn"
	}
	actor := spawn.Actor
	if actor == "" {
		actor = "agent"
	}
	Append(ctx, base, Event{
		Ts:           time.Now().UnixMilli(),
		Kind:         KindAgentSpawn,
		Actor:        actor,
		Host:         spawn.Host,
		Session:      spawn.Session,
		Workspace:    spawn.Workspace,
		Action:       action,
		Lease:        lease,
		Outcome:      OutcomeOK,
		RequestRef:   reqRef,
		RequestBytes: reqBytes,
	})
}

// LeaseFromEnv returns the lease this process is ACTING as, or "" when it claims
// none: the magus.lease member of the W3C BAGGAGE channel. See [SpawnFromEnv], which
// reads the rest of what that environment claimed.
//
// This is the second of the two lease channels, and the two say different things. The
// lease marker (see leaseFromContext) is the ORCHESTRATOR's assertion about a handoff
// it is making; the environment is the WORKER's own claim about itself. Where both are
// available the marker wins: the party doing the partitioning is the one that knows the
// partition.
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
// First line, not anywhere in the head: a lease prompt routinely quotes things - a
// ledger listing, a file, another agent's transcript - and a marker line lifted from any
// of them would stamp the event with a lease that has nothing to do with this
// handoff. A marker an orchestrator wrote is at the top, and a marker in quoted prose is
// not; the position is the only thing that separates them. Leading blank lines are
// formatting and are skipped.
//
// The id itself has to satisfy [types.ValidLeaseID], which is where the charset and
// its reasoning live.
//
// Anything else - no marker, an empty id, an id carrying spaces or punctuation outside
// the set, a marker below the first line - yields "". Correlation is cooperative: a
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
		if !types.ValidLeaseID(id) {
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
// deletes the file - strictly worse than the same value reaching a terminal. It took a ctx
// parameter for no other reason.
func Append(ctx context.Context, base string, e Event) {
	if base == "" {
		return
	}
	e = redactEvent(ctx, e)
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	d := filepath.Join(base, dir)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(d, eventsFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
	_ = f.Close()
}

// WriteBlob stores a payload under a provenance prefix (e.g. "mcp") and returns its ref and byte
// size. An empty base, invalid prefix, empty data, or write failure yields an empty ref (the
// caller omits it) while still reporting the size. Idempotent by content and atomic (temp file
// then rename), so a concurrent reader never observes a partial blob.
func WriteBlob(ctx context.Context, base, prefix string, data []byte) (ref string, size int64) {
	// Redacted BEFORE the ref is computed, so the content address names what is actually
	// stored. Blobs are the highest-risk surface in this package: an MCP request/response pair
	// is a whole tool payload, persisted verbatim, and nothing else on the path scrubs it.
	data = secret.Redact(ctx, data)
	size = int64(len(data))
	if base == "" || len(data) == 0 || !validPrefix(prefix) {
		return "", size
	}
	d := blobsPath(base)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return "", size
	}
	ref = blobRef(prefix, data)
	path := filepath.Join(d, ref)
	if _, err := os.Stat(path); err == nil {
		return ref, size // already stored
	}
	tmp, err := os.CreateTemp(d, ref+".*")
	if err != nil {
		return "", size
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", size
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return "", size
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", size
	}
	return ref, size
}

// ReadBlob returns a stored payload by ref, for the ActivityService to serve. The ref is
// validated as a bare provenance-prefixed hash before it touches the filesystem, and the read
// creates nothing.
func ReadBlob(base, ref string) ([]byte, error) {
	if !validRef(ref) {
		return nil, fmt.Errorf("trail: invalid ref %q: expected a 2-8 letter provenance prefix (e.g. %q) followed by %d hex chars", ref, "mcp", refHexLen)
	}
	return os.ReadFile(filepath.Join(blobsPath(base), ref))
}

// LastRun returns the most recent KIND_JOB event whose Action equals action - the space-joined
// worker argv recorded for a background job (e.g. "graph build") - and whether one was found.
// It is how a caller shows a job's last outcome. Scans the retained trail newest-first; a job
// that has not run within it is (zero, false).
func LastRun(base, action string) (Event, bool) {
	events, _ := ReadRecent(base, maxEvents)
	for _, e := range events { // newest-first
		if e.Kind == KindJob && e.Action == action {
			return e, true
		}
	}
	return Event{}, false
}

// Stat reports the trail's current on-disk footprint under base: total bytes (the events file
// plus every payload blob) and the number of recorded events. It is what a caller shows to
// judge whether a rotate is worth running. Best-effort and read-only: a missing or empty trail
// is (0, 0), and an unreadable directory is skipped rather than erroring - a size readout is
// never a precondition for anything.
func Stat(base string) (bytes int64, count int64) {
	if base == "" {
		return 0, 0
	}
	if f, err := os.Open(eventsPath(base)); err == nil {
		if fi, err := f.Stat(); err == nil {
			bytes += fi.Size()
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
				bytes += info.Size()
			}
		}
	}
	return bytes, count
}

// ReadRecent returns up to limit events from the tail of the trail, newest first. A missing or
// empty trail yields no events and no error. The file is opened read-only, so it is safe to call
// while a producer appends. A corrupt line is skipped, not fatal.
func ReadRecent(base string, limit int) ([]Event, error) {
	if base == "" || limit <= 0 {
		return nil, nil
	}
	f, err := os.Open(eventsPath(base))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	// A fixed-size window over the tail: keep the last `limit` lines in a circular buffer (head
	// marks the oldest slot) so a long file costs O(N) scan and O(limit) memory, no per-line shift.
	buf := make([]string, 0, limit)
	head := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // ref lines stay small; allow headroom
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if len(buf) < limit {
			buf = append(buf, line)
		} else {
			buf[head] = line
			head = (head + 1) % limit
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	out := make([]Event, 0, len(buf))
	for i := 0; i < len(buf); i++ { // oldest -> newest
		var e Event
		if json.Unmarshal([]byte(buf[(head+i)%len(buf)]), &e) == nil {
			out = append(out, e)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // reverse to newest-first
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// Rotate keeps the last maxEvents events and deletes blobs that no kept event references.
// Best-effort: any error leaves the trail as-is, and it only rewrites when the file exceeds the
// cap. It takes no lock, so a concurrent Append racing the read-rewrite window can be dropped -
// acceptable for a best-effort governance trail, and the price of keeping the trail lock-free.
//
// It is CHEAP to call on a trail that is already small (see minEventBytes), which is what lets the
// daemon's maintenance schedule be the single owner of rotation. A second, write-triggered path -
// a rotate driven off a producer's own append counter - can only cover that one producer: an agent
// hook is a short-lived process with nowhere to keep a counter, so a hook-fed trail would be
// write-bounded by nothing at all. One trigger that every producer shares beats two that disagree
// about who is covered.
func Rotate(base string) { rotate(base, maxEvents) }

// minEventBytes is a floor on one serialized event line, used to skip the read entirely when the
// file is too small to hold maxEvents of them. It is a SOUND bound rather than a guess: Ts, Kind,
// Actor, Action and Outcome have no omitempty, so even an all-empty event marshals to about 65
// bytes plus a newline. Rounding down to 64 keeps the check conservative - it can only ever decide
// to look when it did not need to, never to skip when it did.
const minEventBytes = 64

// perKindFloor is how many of a kind's newest events survive a rotate regardless of how loud
// its neighbors are.
//
// Plain recency is the wrong policy for a record with kinds this uneven. One chatty producer -
// an agent hook wired to a read tool is the obvious one, but MCP tool calls do it too - can push
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
// Kind is read with a narrow decode rather than a full Event unmarshal - this runs over the whole
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
		if err := json.Unmarshal([]byte(l), &lk); err != nil {
			continue
		}
		kindOf[i] = lk.Kind
		if lk.Kind != "" {
			present[lk.Kind] = true
		}
	}

	// The reservation is the floor OR an equal share of the window, whichever is smaller.
	// Without the share, a floor larger than the window lets the first pass spend the whole
	// budget on whichever kind happens to be newest - which is precisely the eviction this
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
	// check reads the whole trail - fine at a 30-day cadence, wasteful at an hourly one, and
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

	tmp, err := os.CreateTemp(filepath.Join(base, dir), eventsFile+".*")
	if err != nil {
		return
	}
	for _, l := range kept {
		if l == "" {
			continue
		}
		if _, err := tmp.WriteString(l + "\n"); err != nil {
			tmp.Close()
			_ = os.Remove(tmp.Name())
			return
		}
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return
	}
	gcBlobs(base, kept)
}

// blobGraceWindow holds back GC on a blob younger than this, even when no kept event
// references it yet. WriteBlob finalizes (renames) a blob before its producer's Append
// writes the event that references it; a rotate landing in that gap would otherwise see
// the blob as unreferenced by the snapshot it just read and delete it out from under the
// still-pending event - the event survives, the payload is gone. Every producer in this
// tree calls WriteBlob immediately followed by Append on the same goroutine (wrap() in
// internal/handler/mcp, AppendAgentCommand above), so the real gap is microseconds; this
// window is deliberately generous to also absorb scheduler preemption, without adding a
// lock that would serialize Append behind rotation.
const blobGraceWindow = 30 * time.Second

// gcBlobs removes blob files that none of the kept event lines reference AND that are
// older than blobGraceWindow. Temp files (from an in-flight WriteBlob) fail validRef and
// are left alone regardless of age.
func gcBlobs(base string, keptLines []string) {
	referenced := make(map[string]struct{})
	for _, l := range keptLines {
		var e Event
		if json.Unmarshal([]byte(l), &e) != nil {
			continue
		}
		if e.RequestRef != "" {
			referenced[e.RequestRef] = struct{}{}
		}
		if e.ResponseRef != "" {
			referenced[e.ResponseRef] = struct{}{}
		}
	}
	entries, err := os.ReadDir(blobsPath(base))
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-blobGraceWindow)
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !validRef(name) {
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

// validPrefix accepts 2 to 8 lowercase letters - a short provenance tag like "mcp".
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

// validRef matches the GetPayload wire pattern: a valid prefix followed by exactly refHexLen hex
// chars. The hash is a FIXED-length suffix, so the split is len-refHexLen - a greedy letter-scan
// would misfire because hex digits a-f are also lowercase letters. It rejects any separator or
// dot, so ReadBlob cannot escape the blob dir.
func validRef(ref string) bool {
	if len(ref) < 2+refHexLen || len(ref) > 8+refHexLen {
		return false
	}
	split := len(ref) - refHexLen
	if !validPrefix(ref[:split]) {
		return false
	}
	for _, c := range ref[split:] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// redactEvent masks every free-text field on an event.
//
// The structural fields are deliberately left alone: Kind, Outcome, Actor, Host, Session,
// Workspace, Lease and the blob refs are enumerated values, identities and content addresses, none
// of which a credential can occupy, and all of which a reader filters on by exact match. Redacting
// them would break the activity view to protect nothing - the same reasoning that leaves slog
// attribute KEYS alone in internal/secret. Lease is the one of those derived from free text - a
// lease prompt, or the BAGGAGE environment channel - rather than supplied by a caller,
// which is why every channel that can stamp one runs it through [types.ValidLeaseID]'s bare-identifier
// rule before it can reach this exemption.
func redactEvent(ctx context.Context, e Event) Event {
	e.Action = secret.RedactString(ctx, e.Action)
	e.Error = secret.RedactString(ctx, e.Error)
	e.Preview = secret.RedactString(ctx, e.Preview)
	return e
}

type baseKey struct{}

// ContextWithBase carries the trail's base directory so a producer deep in the run - a
// magusfile binding, say - can record an event without being handed the path.
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
