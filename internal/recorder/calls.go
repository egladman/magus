package recorder

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/types"
)

// dir is the base-dir subdirectory holding the activity log, beside runs/.
const dir = "activity"

const (
	eventsFile  = "events.jsonl" // append-only JSONL, one Event per line
	blobsSubDir = "blobs"        // content-addressed request/response payloads
)

// refHexLen copies libs/safetyscissors/journal's: how many hex chars of a
// payload's SHA-256 name it. 64 bits is ample against one machine's
// collisions and short in a ref.
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
	Phases        []journal.Phase
	// GuardMs is how long the guard took to judge the call. A pre-call
	// hook never sees the run time, so this is not tool time.
	GuardMs int64
	// ToolMs is the run time the host reports on its post-call hook. It
	// becomes the event's duration_ms. Zero when the host reports none.
	ToolMs int64
	// AgentFacts are the caller's cost facts, recorded in the request blob.
	types.AgentFacts
}

// agentCommandAPIVersion versions the two payloads an agent command keeps.
const agentCommandAPIVersion types.APIVersion = "magus.agent-command/v1alpha1"

// AgentCommandRequest is the request payload of an agent command: what the
// host asked to run, restated by magus.
type AgentCommandRequest struct {
	APIVersion types.APIVersion `json:"api_version"`
	Host       string           `json:"host,omitempty"`
	Session    string           `json:"session,omitempty"`
	Transcript string           `json:"transcript,omitempty"`
	Event      string           `json:"event,omitempty"`
	Tool       string           `json:"tool,omitempty"`
	Command    string           `json:"command,omitempty"`
	Path       string           `json:"path,omitempty"`
	// Additive for the reason AgentCommandResponse.PreauthorizedBy gives.
	types.AgentFacts
}

// AgentCommandResponse is the response payload of an agent command: how the
// guard answered.
type AgentCommandResponse struct {
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

// RetiredDecisionError is an agent-command blob holding "pass" or "allow",
// the names the guard's defer had before its decisions became defer, advise,
// ask and deny. A reader refuses the store rather than drop the observation
// from what it counts.
type RetiredDecisionError struct {
	// Store is the activity directory the blob was read from; Ref the blob.
	Store    string
	Ref      string
	Decision string
}

func (e *RetiredDecisionError) Error() string {
	return fmt.Sprintf("recorder: %s holds the retired guard decision %q in payload %s: clear that store (remove the directory) or start a fresh session", e.Store, e.Decision, e.Ref)
}

// BlobUndecodableError is an agent-command blob that is there and does not
// decode. A reader returns it beside what the other blobs held, rather than
// count around it.
type BlobUndecodableError struct {
	// Store is the activity directory the blob was read from; Ref the blob.
	Store string
	Ref   string
	Err   error
}

func (e *BlobUndecodableError) Error() string {
	return fmt.Sprintf("recorder: payload %s in %s does not decode, so what it recorded is not counted: %v", e.Ref, e.Store, e.Err)
}

func (e *BlobUndecodableError) Unwrap() error { return e.Err }

// DecodeRequestBlob decodes the request blob ref, read from the activity log
// under base, into req. A retired decision is a [*RetiredDecisionError] and
// any other failure a [*BlobUndecodableError].
func DecodeRequestBlob(base, ref string, raw []byte, req *AgentCommandRequest) error {
	err := DecodeAgentBlob(base, ref, raw, req)
	var retired *RetiredDecisionError
	if err == nil || errors.As(err, &retired) {
		return err
	}
	return &BlobUndecodableError{Store: callsDir(base), Ref: ref, Err: err}
}

// retiredDecisions are the decision names no build after the defer rename writes.
func retiredDecisions() []string { return []string{"pass", "allow"} }

// DecodeAgentBlob decodes the agent-command blob ref, read from the activity
// log under base, into v. A retired decision is a [*RetiredDecisionError];
// any other failure is returned as is.
func DecodeAgentBlob(base, ref string, raw []byte, v any) error {
	err := codec.Unmarshal(raw, v)
	var unknown *verdict.UnknownDecisionError
	if errors.As(err, &unknown) && slices.Contains(retiredDecisions(), unknown.Decision) {
		return &RetiredDecisionError{Store: callsDir(base), Ref: ref, Decision: unknown.Decision}
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
	requestBody, _ := codec.Marshal(AgentCommandRequest{
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
	responseBody, _ := codec.Marshal(AgentCommandResponse{
		APIVersion:      agentCommandAPIVersion,
		Decision:        command.Decision,
		Reason:          command.Reason,
		Context:         command.Context,
		Rule:            command.Rule,
		PreauthorizedBy: command.PreauthorizedBy,
		RuleFailures:    redactFailures(ctx, command.RuleFailures),
	})
	request := WritePayload(ctx, base, "agent", requestBody)
	response := WritePayload(ctx, base, "agent", responseBody)

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
	AppendCall(ctx, base, journal.Call{
		Time:      time.Now(),
		Kind:      journal.CallKindAgentCommand,
		Origin:    hookOrigin(job.Origin{EntryPoint: command.EntryPoint, Host: command.Host, Session: command.Session, Agent: command.Agent}),
		Workspace: command.Workspace,
		Name:      action,
		Lease:     lease,
		LeaseFrom: leaseFrom,
		Outcome:   journal.OutcomeOK,
		Duration:  time.Duration(command.ToolMs) * time.Millisecond,
		Request:   request,
		Response:  response,
		Preview:   preview,
		Guard: judged(journal.Guard{
			Duration:      time.Duration(command.GuardMs) * time.Millisecond,
			Verdict:       journal.Payload{Ref: command.VerdictRef},
			PolicyDigest:  command.PolicyDigest,
			DecidedBy:     command.DecidedBy,
			Binary:        command.Binary,
			BinaryVersion: command.BinaryVersion,
			Cwd:           command.Cwd,
			Phases:        command.Phases,
		}),
	})
}

// judged is g, or nil when g records nothing: no guard judged the call.
func judged(g journal.Guard) *journal.Guard {
	if g.Duration == 0 && g.Verdict.Ref == "" && g.PolicyDigest == "" && g.DecidedBy == "" &&
		g.Binary == "" && g.BinaryVersion == "" && g.Cwd == "" && len(g.Phases) == 0 {
		return nil
	}
	return &g
}

// verdictRef is the ref of the verdict that judged c, empty when none did.
func verdictRef(c journal.Call) string {
	if c.Guard == nil {
		return ""
	}
	return c.Guard.Verdict.Ref
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
	Phases        []journal.Phase
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

// agentSpawnAPIVersion versions an agent spawn's request payload.
const agentSpawnAPIVersion types.APIVersion = "magus.agent-spawn/v1alpha1"

// AgentSpawnRequest is the request payload of an agent spawn: the context
// the caller handed its subagent.
type AgentSpawnRequest struct {
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
	// bump, for the reason AgentCommandResponse.PreauthorizedBy already documents.
	DeclaredModel string `json:"declared_model,omitempty"`
	// Target and RuleFailures are additive for the reason DeclaredModel is.
	Target       string        `json:"target,omitempty"`
	RuleFailures []RuleFailure `json:"rule_failures,omitempty"`
	types.AgentFacts
}

const (
	// nameAgentSpawn is the action of an agent_spawn event whose host named no child.
	nameAgentSpawn = "agent.spawn"
	// NameAgentContinue is the action of an agent_spawn event recording a message to a
	// subagent that already exists.
	NameAgentContinue = "agent.continue"
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
	body, _ := codec.Marshal(AgentSpawnRequest{
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
	request := WritePayload(ctx, base, "spawn", body)

	// The CHILD is the action, the way an MCP call's action is its tool name: it is the field a
	// reader groups a page of leases by. A host that supplied no label leaves the generic
	// verb, so the row still says what happened. A continuation starts no child, so it is
	// always its own verb and a spawn count can leave it out.
	action := spawn.Child
	switch {
	case spawn.Continue:
		action = NameAgentContinue
	case action == "":
		action = nameAgentSpawn
	}
	AppendCall(ctx, base, journal.Call{
		Time:      time.Now(),
		Kind:      journal.CallKindAgentSpawn,
		Origin:    hookOrigin(job.Origin{EntryPoint: spawn.EntryPoint, Host: spawn.Host, Session: spawn.Session, Agent: spawn.Agent}),
		Workspace: spawn.Workspace,
		Name:      action,
		Lease:     lease,
		Outcome:   journal.OutcomeOK,
		Request:   request,
		Guard: judged(journal.Guard{
			Verdict:       journal.Payload{Ref: spawn.VerdictRef},
			PolicyDigest:  spawn.PolicyDigest,
			DecidedBy:     spawn.DecidedBy,
			Binary:        spawn.Binary,
			BinaryVersion: spawn.BinaryVersion,
			Cwd:           spawn.Cwd,
			Phases:        spawn.Phases,
		}),
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

// callsDir is the directory under base that holds the activity log: what
// journal.Calls and (journal.Payload).Read take.
func callsDir(base string) string { return filepath.Join(base, dir) }

// ReadCalls returns up to limit calls from the end of the activity log under
// base, newest first, by the error rule journal.Calls states. It takes the
// base; journal.Calls takes the directory under it.
func ReadCalls(ctx context.Context, base string, limit int) iter.Seq2[journal.Call, error] {
	return journal.Calls(ctx, callsDir(base), limit)
}

// ReadCallPayload returns the body p names in the activity log under base. A
// ref journal.ValidPayloadRef refuses wraps fs.ErrNotExist.
func ReadCallPayload(base string, p journal.Payload) ([]byte, error) {
	return p.Read(callsDir(base))
}

// The tool labels an agent command's request names; see cmd/magus/agent.go.
// They are magus's own, never a host's tool names.
const (
	ToolFileWrite    = "file.write"
	ToolFileRead     = "file.read"
	ToolShellCommand = "shell.command"
)

// AgentActivity is the newest guard observation one checkout's log holds:
// when a host last ran something past the guard here, and in which session.
// A turn that ran no tool leaves nothing, so a session is never colder than
// this says.
type AgentActivity struct {
	Session string
	Host    string
	At      time.Time
}

// LastAgentActivity returns the newest agent call in the log under base. It
// reports false for a log that is absent, unreadable or holds no agent call,
// and for a ctx that ends first. It decodes one line: a hook asks on every
// call, and the rest of the file is matched by substring.
func LastAgentActivity(ctx context.Context, base string) (AgentActivity, bool) {
	if base == "" {
		return AgentActivity{}, false
	}
	f, err := os.Open(eventsPath(base))
	if err != nil {
		return AgentActivity{}, false
	}
	defer f.Close()

	newest := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if ctx.Err() != nil {
			return AgentActivity{}, false
		}
		if line := sc.Text(); strings.Contains(line, agentKindTag) {
			newest = line
		}
	}
	if sc.Err() != nil || newest == "" {
		return AgentActivity{}, false
	}
	var e journal.Call
	if e.UnmarshalJSON([]byte(newest)) != nil {
		return AgentActivity{}, false
	}
	if e.Kind != journal.CallKindAgentCommand && e.Kind != journal.CallKindAgentSpawn || e.Time.IsZero() || e.Time.UnixMilli() <= 0 {
		// A missing time would clock to 1970 and print half a century of age.
		return AgentActivity{}, false
	}
	return AgentActivity{Session: e.Session, Host: e.Host, At: e.Time}, true
}

// agentKindTag is the encoded prefix both agent kinds share.
const agentKindTag = `"kind":"agent_`

// Relativize makes a recorded path relative to the workspace root, the form a
// review speaks. A path already relative, or outside root, comes back
// unchanged, so /repo2/x stays as it is under the root /repo.
func Relativize(root, p string) string {
	if p == "" || root == "" {
		return p
	}
	if rel, ok := strings.CutPrefix(p, strings.TrimSuffix(root, "/")+"/"); ok {
		return rel
	}
	return p
}

// ObservedCounts reports how many reads, writes and shell commands the
// newest limit calls under base observed. It answers whether the observer
// records at all. A log that cannot be read is the error. A request payload
// that does not decode is left out and returned in the joined error.
func ObservedCounts(ctx context.Context, base string, limit int) (reads, writes, shell int, err error) {
	events, err := RecentCalls(ctx, base, limit)
	if err != nil {
		return 0, 0, 0, err
	}
	var errs []error
	for _, e := range events {
		if e.Kind != journal.CallKindAgentCommand || e.Request.Ref == "" {
			continue
		}
		raw, rerr := ReadCallPayload(base, e.Request)
		if rerr != nil {
			continue
		}
		var req AgentCommandRequest
		if err := DecodeRequestBlob(base, e.Request.Ref, raw, &req); err != nil {
			errs = append(errs, err)
			continue
		}
		switch req.Tool {
		case ToolFileRead:
			reads++
		case ToolFileWrite:
			writes++
		case ToolShellCommand:
			shell++
		}
	}
	return reads, writes, shell, errors.Join(errs...)
}

// CommandProgram reduces a recorded command line to the program it ran and
// drops every argument, where a credential would sit. Leading VAR=value
// assignments are skipped for the same reason. A line it cannot read
// reduces to "", since a guessed program would be an invented fact.
func CommandProgram(cmd string) string {
	for _, tok := range strings.Fields(cmd) {
		if isEnvAssignment(tok) {
			continue
		}
		if i := strings.LastIndexAny(tok, `/\`); i >= 0 {
			tok = tok[i+1:]
		}
		if tok == "" {
			continue
		}
		if len(tok) > commandProgramMax {
			tok = tok[:commandProgramMax]
		}
		return tok
	}
	return ""
}

// commandProgramMax bounds a reported program name.
const commandProgramMax = 32

// isEnvAssignment reports the NAME=value shape a shell reads as an
// assignment before the command, not any token holding "=".
func isEnvAssignment(tok string) bool {
	i := strings.IndexByte(tok, '=')
	if i <= 0 {
		return false
	}
	for j := 0; j < i; j++ {
		c := tok[j]
		switch {
		case c == '_', c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
		case j > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}
	return true
}

// RecentCalls is up to limit calls from the end of the log under base, newest
// first. A line that does not decode is passed over; any other read failure
// is the error, and so is an empty base.
func RecentCalls(ctx context.Context, base string, limit int) ([]journal.Call, error) {
	if base == "" {
		return nil, errors.New("recorder: read names no directory")
	}
	var out []journal.Call
	for c, err := range ReadCalls(ctx, base, limit) {
		if errors.Is(err, codec.ErrLineUndecodable) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

func eventsPath(base string) string { return filepath.Join(base, dir, eventsFile) }
func blobsPath(base string) string  { return filepath.Join(base, dir, blobsSubDir) }

// AppendCall records c in the activity log under base. An empty base or an
// I/O error is dropped: the log is never a precondition for the call. Each
// append is one POSIX append of one short line, so writers do not
// interleave. Every free-text field is redacted through ctx's resolver first.
func AppendCall(ctx context.Context, base string, c journal.Call) {
	if base == "" {
		return
	}
	c = redactCall(ctx, c)
	c.Origin = StampOrigin(ctx, c.Origin)
	line, err := c.MarshalJSON()
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

// WritePayload stores data under a provenance prefix such as "mcp" and
// returns the payload that names it. An empty base, a bad prefix, empty data
// or a failed write gives a payload with no ref and its size still set. The
// write is idempotent by content and replaces whole, so a concurrent reader
// never sees a partial payload.
func WritePayload(ctx context.Context, base, prefix string, data []byte) journal.Payload {
	// Redacted BEFORE the ref is computed, so the content address names what is actually
	// stored. Blobs are the highest-risk data in this package: an MCP request/response pair
	// is a whole tool payload, persisted verbatim, and nothing else on the path scrubs it.
	data = secrets.Redact(ctx, data)
	unkept := journal.Payload{Bytes: int64(len(data))}
	if base == "" || len(data) == 0 || !validPrefix(prefix) {
		return unkept
	}
	kept := journal.Payload{Ref: blobRef(prefix, data), Bytes: unkept.Bytes}
	path := filepath.Join(blobsPath(base), kept.Ref)
	if _, err := os.Stat(path); err == nil {
		// A dedup hit is a fresh reference to an old file, and gcBlobs decides by mtime: without
		// this touch a rotate between here and the caller's Append sees a blob older than
		// blobGraceWindow that no kept event names yet, and deletes a payload about to go live.
		now := time.Now()
		_ = os.Chtimes(path, now, now)
		return kept
	}
	// 0600 because a blob is a whole tool payload, which redaction scrubs of what it
	// recognizes and no more.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return unkept
	}
	if err := file.ReplaceFile(path, data, 0o600); err != nil {
		return unkept
	}
	return kept
}

// LastJob returns the newest job call whose Name is name, the space-joined
// argv of a background job such as "graph build", and whether there is one.
// A job that has not run within the retained log is (zero, false, nil); a
// log that cannot be read is the error.
func LastJob(ctx context.Context, base, name string) (journal.Call, bool, error) {
	calls, err := RecentCalls(ctx, base, maxEvents)
	if err != nil {
		return journal.Call{}, false, err
	}
	for _, c := range calls { // newest-first
		if c.Kind == journal.CallKindJob && c.Name == name {
			return c, true, nil
		}
	}
	return journal.Call{}, false, nil
}

// StatCalls reports the activity log's footprint under base: the bytes of
// the log and every payload, and the number of calls. An empty base or a
// missing log or payload directory adds nothing; any other read failure is
// the error.
func StatCalls(ctx context.Context, base string) (size, count int64, err error) {
	if base == "" {
		return 0, 0, nil
	}
	size, count, err = statLog(ctx, eventsPath(base))
	if err != nil {
		return 0, 0, err
	}
	entries, err := os.ReadDir(blobsPath(base))
	if errors.Is(err, fs.ErrNotExist) {
		return size, count, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("recorder: %w", err)
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		info, err := ent.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return 0, 0, fmt.Errorf("recorder: %w", err)
		}
		size += info.Size()
	}
	return size, count, nil
}

// statLog is the bytes and non-blank lines of the log at path; a missing log
// has none.
func statLog(ctx context.Context, path string) (size, count int64, err error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("recorder: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, 0, fmt.Errorf("recorder: %w", err)
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		if len(sc.Bytes()) > 0 {
			count++
		}
	}
	if err := sc.Err(); err != nil {
		return 0, 0, fmt.Errorf("recorder: read %s: %w", path, err)
	}
	return fi.Size(), count, nil
}

// RotateCalls keeps the newest maxEvents calls and deletes payloads no kept
// call names. An error leaves the log as it was, and a payload it could not
// delete is kept. It takes no lock, so an append racing the rewrite can be
// dropped. A small log costs one stat, so the server's schedule is the only
// trigger: a hook process has nowhere to keep an append counter.
func RotateCalls(ctx context.Context, base string) error {
	return rotate(ctx, base, maxEvents)
}

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

// rotate keeps the newest keep calls under base by selectKept's policy. An
// empty base or a missing log is nothing to rotate.
func rotate(ctx context.Context, base string, keep int) error {
	if base == "" {
		return nil
	}
	path := eventsPath(base)
	// A stat before the read is what makes a frequent schedule affordable. Without it, every
	// check reads the whole trail: fine at a 30-day cadence, wasteful at an hourly one, and
	// worst exactly when the file has grown large enough to matter.
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("recorder: rotate: %w", err)
	}
	if info.Size() < int64(keep)*minEventBytes {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("recorder: rotate: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) <= keep {
		return nil
	}
	kept := selectKept(lines, keep)

	var b strings.Builder
	for _, l := range kept {
		if l != "" {
			b.WriteString(l + "\n")
		}
	}
	// Synced, unlike a blob: a crash that emptied this file would lose the whole trail,
	// where an unsynced blob loses one payload.
	if err := file.WriteFileAtomic(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("recorder: rotate: %w", err)
	}
	return gcBlobs(base, kept)
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

// gcBlobs removes the blobs no kept line references that are older than
// blobGraceWindow. An in-flight WritePayload's temp file fails
// journal.ValidPayloadRef and stays. A blob it cannot remove is the error.
func gcBlobs(base string, keptLines []string) error {
	referenced := make(map[string]struct{})
	for _, l := range keptLines {
		var c journal.Call
		if c.UnmarshalJSON([]byte(l)) != nil {
			continue
		}
		refs := []string{c.Request.Ref, c.Response.Ref}
		if c.Guard != nil {
			refs = append(refs, c.Guard.Verdict.Ref)
		}
		for _, ref := range refs {
			if ref != "" {
				referenced[ref] = struct{}{}
			}
		}
	}
	entries, err := os.ReadDir(blobsPath(base))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("recorder: collect payloads: %w", err)
	}
	cutoff := time.Now().Add(-blobGraceWindow)
	var errs []error
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || !journal.ValidPayloadRef(name) {
			continue
		}
		if _, ok := referenced[name]; ok {
			continue
		}
		info, err := ent.Info()
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if info.ModTime().After(cutoff) {
			continue // too young: its event may not have landed yet
		}
		if err := os.Remove(filepath.Join(blobsPath(base), name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
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

// redactCall masks every free-text field on a call. The structural fields stay
// as written, since a reader matches them exactly. A lease comes from free
// text, so every channel that stamps one checks it with [job.ValidID] first.
func redactCall(ctx context.Context, c journal.Call) journal.Call {
	c.Name = secrets.RedactString(ctx, c.Name)
	c.Error = secrets.RedactString(ctx, c.Error)
	c.Preview = secrets.RedactString(ctx, c.Preview)
	return c
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
