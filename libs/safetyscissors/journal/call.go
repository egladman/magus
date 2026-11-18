package journal

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

const (
	callsFile  = "events.jsonl"
	payloadDir = "blobs"
)

// refHexLen is how many hex chars of a payload's SHA-256 name its blob. 16 (64 bits) is
// ample against content collisions among one machine's payloads while staying short in
// a ref.
const refHexLen = 16

// CallKind names where a [Call] came from. The set grows without a schema
// change, and a reader keeps a Call whose kind it does not know.
type CallKind string

const (
	CallKindMCPTool        CallKind = "mcp_tool_call"
	CallKindJob            CallKind = "job"             // a server background job
	CallKindConfigChange   CallKind = "config_change"   // magus.yaml changed, or a config set
	CallKindTokenLifecycle CallKind = "token_lifecycle" // a connector token minted or revoked
	CallKindSandboxDenial  CallKind = "sandbox_denial"  // the policy check refused an access
	// CallKindAgentCommand is observed before the host ran it: a request and a
	// decision, never an exit status.
	CallKindAgentCommand CallKind = "agent_command"
	// CallKindAgentSpawn is work handed to a subagent; the handed context is
	// the request payload. A lease joins only from a "lease: <id>" first line.
	CallKindAgentSpawn CallKind = "agent_spawn"
	CallKindNotes      CallKind = "notes" // a read of the workspace's notes
	// CallKindCredentialGrant is a credential made reachable: the reference,
	// the host and the header, never the value.
	CallKindCredentialGrant CallKind = "credential_grant"
	// CallKindGuardPolicy is a change to the effective workspace guard rules,
	// written only on a change.
	CallKindGuardPolicy CallKind = "guard_policy"
)

// Outcome is whether a call succeeded.
type Outcome string

const (
	OutcomeOK    Outcome = "ok"
	OutcomeError Outcome = "error"
)

// Call is one line of the activity log: who did what, and whether it
// succeeded. A large body never rides the line: Request and Response name
// payloads beside the log.
type Call struct {
	Time       time.Time // "ts" on disk, unix milliseconds at the call's start
	Kind       CallKind
	job.Origin           // who acted, one field per channel; inline on disk
	UserAgent  string
	Workspace  string // the root the call pertained to; empty for a server-wide one
	Name       string // the specific call: a tool name, a job command; "action" on disk
	Lease      string // the lease the call belongs to; empty when uncorrelated
	LeaseFrom  job.LeaseSource
	Outcome    Outcome
	Error      string
	Duration   time.Duration // "duration_ms"
	Request    Payload       // "request_ref", "request_bytes"
	Response   Payload       // "response_ref", "response_bytes"
	Preview    string        // the response's opening characters
	// Guard is how the call was judged, nil when no guard judged it.
	Guard *Guard
}

// Guard is how a guard judged a call: how long it took, under which policy,
// by which binary. The answer itself is a verdict.Judgment, held in the
// Verdict payload.
type Guard struct {
	Duration      time.Duration // "guard_ms"
	Verdict       Payload       // "verdict_ref"; its Bytes is not recorded
	PolicyDigest  string
	DecidedBy     string // builtin, worktree or approved, joined with "+"
	Binary        string
	BinaryVersion string
	Cwd           string
	Phases        []Phase
}

// Phase is one timed step of a guard's judgment.
type Phase struct {
	Name      string `json:"name"`
	ElapsedMs int64  `json:"elapsed_ms"`
	BudgetMs  int64  `json:"budget_ms"` // 0 when the step had none
	Lapsed    bool   `json:"lapsed"`
}

// Payload names a body stored beside the activity log. Ref is empty when none
// was kept; Bytes is the body's full length either way.
type Payload struct {
	Ref   string
	Bytes int64
}

// callLine is the activity line as the writer lays it out. Its fields, tags
// and order are the on-disk format.
type callLine struct {
	Ts            int64           `json:"ts"`
	Kind          CallKind        `json:"kind"`
	UserAgent     string          `json:"user_agent,omitempty"`
	job.Origin    `json:",inline"`
	Workspace     string          `json:"workspace,omitempty"`
	Action        string          `json:"action"`
	Lease         string          `json:"lease,omitempty"`
	LeaseFrom     job.LeaseSource `json:"lease_from,omitempty"`
	Outcome       Outcome         `json:"outcome"`
	Error         string          `json:"error,omitempty"`
	DurationMs    int64           `json:"duration_ms"`
	GuardMs       int64           `json:"guard_ms"`
	RequestRef    string          `json:"request_ref,omitempty"`
	ResponseRef   string          `json:"response_ref,omitempty"`
	Preview       string          `json:"preview,omitempty"`
	RequestBytes  int64           `json:"request_bytes"`
	ResponseBytes int64           `json:"response_bytes"`
	VerdictRef    string          `json:"verdict_ref,omitempty"`
	PolicyDigest  string          `json:"policy_digest,omitempty"`
	DecidedBy     string          `json:"decided_by,omitempty"`
	Binary        string          `json:"binary,omitempty"`
	BinaryVersion string          `json:"binary_version,omitempty"`
	Cwd           string          `json:"cwd,omitempty"`
	Phases        []Phase         `json:"phases,omitempty"`
}

func (c Call) line() callLine {
	l := callLine{
		Ts:            unixMilli(c.Time),
		Kind:          c.Kind,
		UserAgent:     c.UserAgent,
		Origin:        c.Origin,
		Workspace:     c.Workspace,
		Action:        c.Name,
		Lease:         c.Lease,
		LeaseFrom:     c.LeaseFrom,
		Outcome:       c.Outcome,
		Error:         c.Error,
		DurationMs:    c.Duration.Milliseconds(),
		RequestRef:    c.Request.Ref,
		ResponseRef:   c.Response.Ref,
		Preview:       c.Preview,
		RequestBytes:  c.Request.Bytes,
		ResponseBytes: c.Response.Bytes,
	}
	if g := c.Guard; g != nil {
		l.GuardMs = g.Duration.Milliseconds()
		l.VerdictRef = g.Verdict.Ref
		l.PolicyDigest = g.PolicyDigest
		l.DecidedBy = g.DecidedBy
		l.Binary = g.Binary
		l.BinaryVersion = g.BinaryVersion
		l.Cwd = g.Cwd
		l.Phases = g.Phases
	}
	return l
}

func (l callLine) call() Call {
	c := Call{
		Time:      fromUnixMilli(l.Ts),
		Kind:      l.Kind,
		Origin:    l.Origin,
		UserAgent: l.UserAgent,
		Workspace: l.Workspace,
		Name:      l.Action,
		Lease:     l.Lease,
		LeaseFrom: l.LeaseFrom,
		Outcome:   l.Outcome,
		Error:     l.Error,
		Duration:  millis(l.DurationMs),
		Request:   Payload{Ref: l.RequestRef, Bytes: l.RequestBytes},
		Response:  Payload{Ref: l.ResponseRef, Bytes: l.ResponseBytes},
		Preview:   l.Preview,
	}
	g := Guard{
		Duration:      millis(l.GuardMs),
		Verdict:       Payload{Ref: l.VerdictRef},
		PolicyDigest:  l.PolicyDigest,
		DecidedBy:     l.DecidedBy,
		Binary:        l.Binary,
		BinaryVersion: l.BinaryVersion,
		Cwd:           l.Cwd,
		Phases:        l.Phases,
	}
	if g.Duration != 0 || g.Verdict.Ref != "" || g.PolicyDigest != "" || g.DecidedBy != "" ||
		g.Binary != "" || g.BinaryVersion != "" || g.Cwd != "" || len(g.Phases) > 0 {
		c.Guard = &g
	}
	return c
}

// LogValue lets a host log c with its own logger, keyed as the line is on
// disk. A payload is logged by its ref, never by its body.
func (c Call) LogValue() slog.Value {
	l := c.line()
	attrs := []slog.Attr{slog.Time("time", c.Time), slog.String("kind", string(l.Kind))}
	attrs = appendString(attrs, "user_agent", l.UserAgent)
	attrs = appendString(attrs, "user", l.User)
	attrs = appendString(attrs, "uid", l.UID)
	attrs = appendString(attrs, "entry_point", string(l.EntryPoint))
	attrs = appendString(attrs, "host", l.Host)
	attrs = appendString(attrs, "session", l.Session)
	attrs = appendString(attrs, "agent", l.Agent)
	attrs = appendString(attrs, "workspace", l.Workspace)
	attrs = append(attrs, slog.String("action", l.Action))
	attrs = appendString(attrs, "lease", l.Lease)
	attrs = appendString(attrs, "lease_from", string(l.LeaseFrom))
	attrs = append(attrs, slog.String("outcome", string(l.Outcome)))
	attrs = appendString(attrs, "error", l.Error)
	attrs = appendInt(attrs, "duration_ms", l.DurationMs)
	attrs = appendInt(attrs, "guard_ms", l.GuardMs)
	attrs = appendString(attrs, "request_ref", l.RequestRef)
	attrs = appendString(attrs, "response_ref", l.ResponseRef)
	attrs = appendInt(attrs, "request_bytes", l.RequestBytes)
	attrs = appendInt(attrs, "response_bytes", l.ResponseBytes)
	attrs = appendString(attrs, "verdict_ref", l.VerdictRef)
	attrs = appendString(attrs, "policy_digest", l.PolicyDigest)
	attrs = appendString(attrs, "decided_by", l.DecidedBy)
	attrs = appendString(attrs, "binary", l.Binary)
	attrs = appendString(attrs, "binary_version", l.BinaryVersion)
	attrs = appendString(attrs, "cwd", l.Cwd)
	return slog.GroupValue(attrs...)
}

// MarshalJSON writes c as its activity line, keys and order as written today.
func (c Call) MarshalJSON() ([]byte, error) { return codec.Marshal(c.line()) }

// UnmarshalJSON reads one activity line. An unknown key is ignored.
func (c *Call) UnmarshalJSON(data []byte) error {
	var l callLine
	if err := codec.Unmarshal(data, &l); err != nil {
		return err
	}
	*c = l.call()
	return nil
}

// Calls returns up to limit calls from the end of the activity log dir holds,
// newest first, in memory proportional to limit. A directory with no log has
// no calls. An empty dir or a limit below 1 is an error. Errors follow the
// rule Records states.
func Calls(ctx context.Context, dir string, limit int) iter.Seq2[Call, error] {
	return func(yield func(Call, error) bool) {
		if dir == "" || limit < 1 {
			yield(Call{}, fmt.Errorf("journal: read calls needs a directory and a limit of at least 1, got %q and %d", dir, limit))
			return
		}
		lines, err := tailLines(ctx, dir, limit)
		if err != nil {
			yield(Call{}, err)
			return
		}
		path := filepath.Join(dir, callsFile)
		for i := len(lines) - 1; i >= 0; i-- {
			var c Call
			if err := codec.Unmarshal(lines[i].text, &c); err != nil {
				if !yield(Call{}, &codec.LineError{Path: path, Line: lines[i].n, Err: err}) {
					return
				}
				continue
			}
			if !yield(c, nil) {
				return
			}
		}
	}
}

type numbered struct {
	n    int
	text []byte
}

// tailLines is the last limit non-blank lines of dir's activity log, oldest
// first. A missing log has none.
func tailLines(ctx context.Context, dir string, limit int) ([]numbered, error) {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}
	defer func() { _ = root.Close() }()
	f, err := root.Open(callsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}
	defer func() { _ = f.Close() }()

	// A circular buffer over the tail; head marks the oldest slot, so no line is shifted.
	buf := make([]numbered, 0, limit)
	head := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // ref lines stay small; allow headroom
	for n := 1; sc.Scan(); n++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(sc.Bytes()) == 0 {
			continue
		}
		line := numbered{n: n, text: append([]byte(nil), sc.Bytes()...)}
		if len(buf) < limit {
			buf = append(buf, line)
		} else {
			buf[head] = line
			head = (head + 1) % limit
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("journal: read %s: %w", filepath.Join(dir, callsFile), err)
	}
	return append(buf[head:], buf[:head]...), nil
}

// ValidPayloadRef reports whether ref names a payload: a 2 to 8 letter
// provenance prefix, then 16 hex characters. The hash is a fixed-length
// suffix, so the split is len-refHexLen: a greedy letter scan would misfire on
// hex digits a-f. It rejects any separator or dot.
func ValidPayloadRef(ref string) bool {
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

// Read returns the body p names under dir. A ref that fails [ValidPayloadRef]
// is refused before it touches the filesystem, with an error that wraps
// fs.ErrNotExist.
func (p Payload) Read(dir string) ([]byte, error) {
	if !ValidPayloadRef(p.Ref) {
		return nil, fmt.Errorf("journal: %q is not a payload ref: %w", p.Ref, fs.ErrNotExist)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("journal: %w", err)
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.Join(payloadDir, p.Ref))
}
