// Package trail is the activity trail: a durable, append-only record of consequential
// actions (who did what, and did it succeed), kept under a directory the caller names.
// The events live in <dir>/activity/events.jsonl, one [Event] per line, and their large
// payloads in a content-addressed blob store beside it, so a line carries a payload's REF
// and never its body.
//
// This package reads the trail and exports no writer: an entry is written only as a
// side effect of the module's own calls, since an entry nobody witnessed would taint
// every other. Every read opens the file read-only, so it is safe beside a producer
// that is appending.
//
// The package writes nothing to stdout or stderr, reads no environment, and keeps no
// process-wide state.
package trail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// activityDir is the subdirectory of the caller's directory holding the trail.
const activityDir = "activity"

const (
	eventsFile  = "events.jsonl"
	blobsSubDir = "blobs"
)

// refHexLen is how many hex chars of a payload's SHA-256 name its blob. 16 (64 bits) is
// ample against content collisions among one machine's payloads while staying short in
// a ref.
const refHexLen = 16

// Kind names an action's source. Readable strings on disk; a NAMED string so a producer
// cannot pass an arbitrary label where a Kind is wanted. A reader must tolerate a kind it
// does not know: the set grows without a schema change.
type Kind string

const (
	KindMCPToolCall    Kind = "mcp_tool_call"
	KindJob            Kind = "job"             // a server background job (SCIP reindex, graph build, VCS refresh)
	KindConfigChange   Kind = "config_change"   // magus.yaml changed on reload, or a config-set mutation
	KindTokenLifecycle Kind = "token_lifecycle" // a connector token minted or revoked
	KindSandboxDenial  Kind = "sandbox_denial"  // the read/write/exec policy check refused an access; a kernel-landlock denial reports nothing back and cannot appear here
	// KindAgentCommand records an agent-host tool invocation observed by a configured hook. It is
	// an observation, not a process result: a PreToolUse hook runs before the host executes the
	// command, so its payload records the requested command or path and the guard's decision, never
	// an invented exit status. MCP calls remain KindMCPToolCall because their wrapper sees completion.
	KindAgentCommand Kind = "agent_command"
	// KindAgentSpawn records that an orchestrating agent handed work to a subagent, and WHAT
	// CONTEXT it handed over. The handed context lands in the request blob and only its REF
	// rides the event, because a lease prompt is routinely kilobytes. A continuation, a
	// message to a subagent that already exists, records here too.
	//
	// Correlation to a lease is COOPERATIVE, not enforced: the event's Lease is stamped only
	// when the handed context's FIRST non-blank line reads "lease: <id>". A missing join is
	// the designed outcome, because a wrong join is worse than none.
	KindAgentSpawn Kind = "agent_spawn"
	// KindNotes is a read of the workspace's human-authored notes. Every event under it is a
	// READ: a note's whole value is the guarantee that a person wrote it. Reads are audited
	// because the PRIVATE store is in no repository and nothing else attributes them.
	KindNotes Kind = "notes"

	// KindGrantIssued records that a run made a credential reachable: a magusfile granted
	// one to a destination host, or opened a loopback endpoint carrying one. It carries the
	// REFERENCE, the host and the header, never the value, which is not resolved at
	// declaration time and must not be resolved to log it.
	KindGrantIssued Kind = "credential_grant"

	// KindGuardPolicy records that the effective workspace guard rules changed: first
	// loaded, tightened by an edit that is live, loosened by one waiting on approval,
	// settled by approval, or removed. Written only on a change, so the lineage of a
	// workspace's policy reads as one row per step. The request blob carries each source
	// path with its working-tree and approved content ids, never a source body.
	KindGuardPolicy Kind = "guard_policy"
)

// Outcome values of [Event.Outcome].
const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
)

// Event is one recorded action, the on-disk atom of the trail. The envelope (Ts/Kind/Origin/
// Action/Outcome) is common to every kind; the payload refs point into the blob store so a large
// body never bloats the line.
//
// Who acted is the [job.Origin], one field per channel, rather than one string a reader must
// guess the kind of. Its Host and Session duplicate what an agent-hook event also records in its
// request blob, and that duplication is deliberate: a reader grouping a page of 200 rows by
// agent host must not have to fetch 200 blobs to do it.
type Event struct {
	Ts         int64  `json:"ts"`                   // unix milliseconds at the action's start
	Kind       Kind   `json:"kind"`                 // one of the Kind* constants
	UserAgent  string `json:"user_agent,omitempty"` // caller's HTTP User-Agent, when known (MCP over HTTP)
	job.Origin `json:",inline"`
	Workspace  string `json:"workspace,omitempty"` // repo-relative or absolute root the action pertained to; "" for server-wide
	Action     string `json:"action"`              // the specific action: a tool name, a job command, "connector.create"
	// Lease is the lease this action belongs to, when the producer could
	// correlate one (a marker line, or the BAGGAGE channel); ""
	// when uncorrelated.
	Lease string `json:"lease,omitempty"`
	// LeaseFrom is which source answered Lease, on the events whose producer resolved one.
	LeaseFrom     job.LeaseSource `json:"lease_from,omitempty"`
	Outcome       string          `json:"outcome"`                // one of the Outcome* constants
	Error         string          `json:"error,omitempty"`        // error text when Outcome is OutcomeError
	DurationMs    int64           `json:"duration_ms"`            // wall-clock, on call-shaped actions; on an agent command, the host-reported tool time
	GuardMs       int64           `json:"guard_ms"`               // how long the guard took to judge an agent command
	RequestRef    string          `json:"request_ref,omitempty"`  // blob ref for the request body (mcp<hash>)
	ResponseRef   string          `json:"response_ref,omitempty"` // blob ref for the response body
	Preview       string          `json:"preview,omitempty"`      // opening characters of the response, for list views
	RequestBytes  int64           `json:"request_bytes"`          // full request length
	ResponseBytes int64           `json:"response_bytes"`
	// VerdictRef is the grd blob holding a guard deny in full, when the reader was shown
	// the short repeat form instead. Named here so rotation keeps the blob the deny cites.
	VerdictRef string `json:"verdict_ref,omitempty"`
	// PolicyDigest names the workspace guard rule set in force when a verdict was reached,
	// the join from a verdict to the guard_policy event that introduced its rules.
	PolicyDigest string `json:"policy_digest,omitempty"`
	// DecidedBy is which side reached a verdict: builtin, worktree, or approved, joined
	// with "+" when a workspace rule added its advice to a built-in one. Empty on a pass,
	// which nothing decided.
	DecidedBy string `json:"decided_by,omitempty"`
	// Binary and BinaryVersion name the binary that judged a hook call, and Cwd the
	// directory it judged the call in: the one the host reported, else the hook process's
	// own. Without them a verdict cannot be tied to the build that reached it, which is the
	// first question when a guard rule that should have fired did not.
	Binary        string `json:"binary,omitempty"`
	BinaryVersion string `json:"binary_version,omitempty"`
	Cwd           string `json:"cwd,omitempty"`
	// Phases are the timed steps of a hook judgment, in the order they ran. A host kills a
	// hook that outlives its own timeout and no row is written, so these are what show
	// where a slow judgment that did finish spent its time.
	Phases []Phase `json:"phases,omitempty"`
}

// Phase is one timed step of a hook judgment.
type Phase struct {
	Name      string `json:"name"`
	ElapsedMs int64  `json:"elapsed_ms"`
	// BudgetMs is the share of the hook's deadline the step had, 0 when it had none.
	BudgetMs int64 `json:"budget_ms"`
	// Lapsed reports that the step ran out of its budget before it answered.
	Lapsed bool `json:"lapsed"`
}

// UnmarshalJSON decodes an event, reading a duration written under either spelling.
//
// compat(until: no trail under <base>/activity still carries "dur_ms"): the duration was
// spelled `dur_ms` before [Event.DurationMs] was. The trail is append-only and rotates, so
// observing that dropping this is safe means finding none left:
//
//	grep -l '"dur_ms"' <base>/activity/events.jsonl
//
// Dropping it early is silent rather than loud: the server's scheduler reads a job's last
// finish as start plus duration, so every pre-rename row reads as having finished the instant
// it started, and a job whose interval has not elapsed is rerun anyway.
func (e *Event) UnmarshalJSON(b []byte) error {
	type event Event // no method set, so this does not recurse
	var decoded event
	if err := codec.Unmarshal(b, &decoded); err != nil {
		return err
	}
	*e = Event(decoded)
	if e.DurationMs == 0 {
		var legacy struct {
			DurMs int64 `json:"dur_ms"`
		}
		if codec.Unmarshal(b, &legacy) == nil {
			e.DurationMs = legacy.DurMs
		}
	}
	return nil
}

func eventsPath(dir string) string { return filepath.Join(dir, activityDir, eventsFile) }
func blobsPath(dir string) string  { return filepath.Join(dir, activityDir, blobsSubDir) }

// InvalidReadError is a [ReadRecent] that names no directory or asks for fewer than one
// event: a caller bug, refused rather than answered with an empty trail.
type InvalidReadError struct {
	Dir   string
	Limit int
}

func (e *InvalidReadError) Error() string {
	if e.Dir == "" {
		return "trail: read names no directory"
	}
	return fmt.Sprintf("trail: read limit %d is below 1", e.Limit)
}

// ReadRecent returns up to limit events from the tail of the trail under dir, newest
// first. A missing or empty trail yields an empty slice and no error; an empty dir or a
// limit below 1 is an [*InvalidReadError]. A corrupt line is skipped, not fatal. Memory
// is O(limit) however long the file is.
//
// It stops early and returns ctx.Err() as is once ctx is done.
func ReadRecent(ctx context.Context, dir string, limit int) ([]Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if dir == "" || limit <= 0 {
		return nil, &InvalidReadError{Dir: dir, Limit: limit}
	}
	f, err := os.Open(eventsPath(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []Event{}, nil
		}
		return nil, fmt.Errorf("trail: open %s: %w", eventsPath(dir), err)
	}
	defer func() { _ = f.Close() }()

	// A circular buffer over the tail; head marks the oldest slot, so no line is shifted.
	buf := make([]string, 0, limit)
	head := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // ref lines stay small; allow headroom
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
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
		return nil, fmt.Errorf("trail: read %s: %w", eventsPath(dir), err)
	}

	out := make([]Event, 0, len(buf))
	for i := range buf { // oldest -> newest
		var e Event
		if codec.Unmarshal([]byte(buf[(head+i)%len(buf)]), &e) == nil {
			out = append(out, e)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // reverse to newest-first
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// InvalidRefError is the refusal of a ref that does not name a blob.
type InvalidRefError struct {
	Ref string
}

func (e *InvalidRefError) Error() string {
	return fmt.Sprintf("trail: invalid ref %q: expected a 2-8 letter provenance prefix (e.g. %q) followed by %d hex chars", e.Ref, "mcp", refHexLen)
}

// ReadBlob returns the payload a ref names in the trail under dir. A ref that fails
// [ValidRef] is an [*InvalidRefError] before it touches the filesystem, and the read
// creates nothing.
func ReadBlob(dir, ref string) ([]byte, error) {
	if !ValidRef(ref) {
		return nil, &InvalidRefError{Ref: ref}
	}
	return os.ReadFile(filepath.Join(blobsPath(dir), ref))
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

// ValidRef reports whether ref names a trail blob (mcp, grd, agent and the rest), so a
// caller can route it to [ReadBlob] without touching the filesystem: a valid prefix
// followed by exactly refHexLen hex chars. The hash is a FIXED-length suffix, so the split
// is len-refHexLen; a greedy letter-scan would misfire because hex digits a-f are also
// lowercase letters. It rejects any separator or dot, so ReadBlob cannot escape the blob
// dir.
func ValidRef(ref string) bool {
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
