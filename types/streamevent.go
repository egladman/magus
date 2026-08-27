package types

import (
	"bytes"
	"fmt"

	json "github.com/egladman/magus/internal/json"
)

// StreamEvent is one fact about a workspace, addressed to a PROGRAM: the record
// an editor plugin, a status bar, or a notifier subscribes to. It is the single
// envelope magus's several internal producers are mapped onto, so an integrator
// learns one shape rather than five.
//
// It is the machine-facing sibling of [Event], and the split is by AUDIENCE, not
// by content. [Event] is what magus emits when it needs a PERSON: it carries an
// urgency tier and a situation class because a human renderer has to decide
// whether to interrupt someone. A StreamEvent carries neither, because a program
// subscribing to a build stream decides relevance by [StreamEventType]. An
// attention request rides this stream as a [StreamAttention] body, which is the
// one place the two meet.
//
// Nothing a subscriber does can change a magus verdict. The stream is outbound
// only, by design and not by omission: docs/scope.md seals the engine, the cache,
// the graph schema, and the guard's evaluation, and an extension seam "may change
// what magus does, never what a verdict means". There is deliberately no reply
// channel here. The inbound counterpart is `magus session hook`, which is
// request/reply and whose verdict this stream can only report after the fact.
//
// Every field carries `json:"-"`: the wire shape comes from [StreamEvent.MarshalJSON]
// and streamHead, not from struct tags here, so a tag on this struct would be a
// second source of truth that nothing reads.
//
// On the wire it is one JSON object per line. The envelope fields come first and
// the body's fields are spliced in flat beside them, so a line reads:
//
//	{"schema":1,"type":"target.result","ts":1756312800000,"workspace":"/repo",
//	 "inv":"inv1a2b3c","project":"cmd/magus","target":"build","status":"fail",...}
//
// Flat rather than nested under a "data" key because that is the shape
// internal/report already ships on `magus run -o jsonl`, and an integrator
// reading both surfaces should not meet two conventions. It also keeps a jq
// filter or an Emacs alist lookup one level deep.
type StreamEvent struct {
	// Ts is the event time in unix milliseconds, matching journal.Event.Ts so a
	// mapped event keeps the timestamp its producer recorded rather than the time
	// the mapping ran.
	Ts int64 `json:"-"`
	// Workspace is the absolute repository root. It is present on every event
	// because a subscriber may watch more than one workspace over a single
	// connection, and an event with no workspace cannot be routed to a buffer.
	Workspace string `json:"-"`
	// Inv groups every event belonging to one magus invocation. Empty for events
	// that belong to no run - a file change, an attention request, a guard verdict.
	Inv string `json:"-"`
	// Body carries the per-type fields and determines [StreamEvent.Type]. It is
	// never nil on a well-formed event; a nil Body marshals as an error rather
	// than as a typeless line.
	Body StreamBody `json:"-"`
}

// StreamSchema is the envelope version, stamped on every line as "schema".
//
// Bump it when a field is renamed or removed, or when an existing field changes
// meaning. Adding a new [StreamEventType], or a new optional field to a body, is
// additive and does NOT bump it: a subscriber is required to ignore types and
// fields it does not recognize, which is what lets magus grow the taxonomy
// without breaking every client.
const StreamSchema = 1

// StreamEventType names the class of fact an event carries. A subscriber
// switches on it; an unrecognized value must be skipped, not treated as an error.
type StreamEventType string

// The taxonomy. Every type maps onto a producer magus already has - the run
// journal, the report writer, the file watcher, the attention store, the activity
// trail - so nothing here required the engine to learn something new.
const (
	// StreamRunStarted opens an invocation. Carries the command lineage, so a
	// subscriber can tell `magus run build` from `magus affected ci`.
	StreamRunStarted StreamEventType = "run.started"
	// StreamRunFinished closes the invocation opened by StreamRunStarted with the
	// same Inv. A subscriber that shows progress clears it here.
	StreamRunFinished StreamEventType = "run.finished"
	// StreamTargetResult reports one target's outcome, cached replays included.
	// It carries the output ref, so a subscriber fetches the captured log on
	// demand instead of buffering every line it was sent.
	StreamTargetResult StreamEventType = "target.result"
	// StreamTargetOutput is one line of a subprocess's stdout or stderr. It is the
	// only high-volume type in the taxonomy and is off unless a subscriber asks
	// for it; see [StreamFilter].
	StreamTargetOutput StreamEventType = "target.output"
	// StreamDiagnostic reports a coded magus finding (MGS####). This is what an
	// editor turns into flycheck or compilation-mode entries.
	StreamDiagnostic StreamEventType = "diagnostic.emitted"
	// StreamWorkspaceChanged reports source files changing on disk. A subscriber
	// uses it to invalidate a cached target list, not to trigger a build: magus
	// does not build on save, and a plugin that does so is making that choice.
	StreamWorkspaceChanged StreamEventType = "workspace.changed"
	// StreamAttentionRaised reports that something is blocked on a person. It is
	// the one type addressed to a human, forwarded so an editor can surface it;
	// disposing it stays manual (docs/doctrine.md, "Manual on purpose").
	StreamAttentionRaised StreamEventType = "attention.raised"
	// StreamGuardVerdict reports a decision `magus session hook` already made. It
	// is an observation of a sealed verdict: a subscriber learns that a command
	// was denied and cannot affect the denial.
	StreamGuardVerdict StreamEventType = "guard.verdict"
)

// StreamBody is the per-type payload of a [StreamEvent]. Implementations are the
// Stream* body structs in this file; the interface is closed in practice, and
// [DecodeStreamEvent] fails on a type it does not know rather than guessing.
//
// Every implementation marshals to a JSON OBJECT. A body that marshals to an
// array or a scalar cannot be spliced flat into the envelope, and
// [StreamEvent.MarshalJSON] reports that as an error rather than emitting a line
// no subscriber can parse.
type StreamBody interface {
	// StreamType reports the type stamped on the wire. It is derived from the body
	// rather than stored on the envelope so the two can never disagree.
	StreamType() StreamEventType
}

// StreamRun is the body of [StreamRunStarted] and [StreamRunFinished].
//
// Started and finished share one body because a subscriber correlates them by
// Inv and reads whichever fields the phase populates: Command and MagusVersion on
// the started event, Status and DurationMs on the finished one. Splitting them
// would double the wire types to save two optional fields.
type StreamRun struct {
	// Phase is "started" or "finished".
	Phase string `json:"phase"`
	// Command is the full argument vector, subcommand included, as invoked.
	// Set on the started phase only.
	Command []string `json:"command,omitempty"`
	// Trigger is how the run was spawned - one of journal's Trigger constants
	// (run, affected, ci, x, watch, direct). Set on the started phase only.
	Trigger string `json:"trigger,omitempty"`
	// MagusVersion is the binary that produced the run. Set on the started phase.
	MagusVersion string `json:"magus_version,omitempty"`
	// Status is the overall outcome, "pass" or "fail". Set on the finished phase.
	Status string `json:"status,omitempty"`
	// DurationMs is the wall-clock span of the whole invocation, in milliseconds.
	// Set on the finished phase.
	DurationMs int64 `json:"duration_ms,omitzero"`
}

// StreamType reports [StreamRunStarted] or [StreamRunFinished] from Phase. An
// unset or unrecognized Phase reports StreamRunFinished, so a malformed body
// still closes a run a subscriber has open rather than leaving a spinner forever.
func (b StreamRun) StreamType() StreamEventType {
	if b.Phase == "started" {
		return StreamRunStarted
	}
	return StreamRunFinished
}

// StreamTarget is the body of [StreamTargetResult]: one target's outcome.
//
// It is emitted for cached targets too, which is what makes CacheHit meaningful:
// Status reports whether the target SUCCEEDED, CacheHit whether it actually ran.
// A subscriber that renders a replay as a fresh build is misreading the pair.
type StreamTarget struct {
	// Project is the workspace-relative project path.
	Project string `json:"project"`
	// Target is the target name as the CLI spells it, charms included.
	Target string `json:"target"`
	// Status is "ok" or "failed". It does NOT distinguish a replay from a run;
	// CacheHit does.
	Status string `json:"status"`
	// CacheHit reports that the result was replayed from cache rather than
	// executed.
	CacheHit bool `json:"cache_hit"`
	// Ref addresses this execution's captured output. A subscriber fetches the
	// full log with `magus query output <ref>` rather than being streamed every
	// line, which is why StreamTargetOutput can stay opt-in.
	Ref string `json:"ref,omitempty"`
	// DurationMs is the target's wall-clock span in milliseconds.
	DurationMs int64 `json:"duration_ms,omitzero"`
	// Error is the failure message when Status is "failed".
	Error string `json:"error,omitempty"`
}

// StreamType implements [StreamBody].
func (StreamTarget) StreamType() StreamEventType { return StreamTargetResult }

// StreamOutput is the body of [StreamTargetOutput]: one line a subprocess wrote.
//
// This is the only type that scales with build size rather than with project
// count, and a busy `affected ci` emits tens of thousands. It is off unless a
// subscriber names it in a [StreamFilter]; an editor that wants a full log should
// fetch it by ref from a [StreamTarget] instead of subscribing here.
type StreamOutput struct {
	Project string `json:"project,omitempty"`
	Target  string `json:"target,omitempty"`
	// Stream is "stdout" or "stderr".
	Stream string `json:"stream"`
	// Text is the line, without its trailing newline.
	Text string `json:"text"`
}

// StreamType implements [StreamBody].
func (StreamOutput) StreamType() StreamEventType { return StreamTargetOutput }

// StreamDiagnosticBody is the body of [StreamDiagnostic]: a coded magus finding.
//
// The name carries the Body suffix because [StreamDiagnostic] is the type
// constant and Go has one namespace for both. Code is an MGS#### documented at
// docs/reference/diagnostics.md, which is what lets a subscriber deep-link a
// finding rather than only printing it.
type StreamDiagnosticBody struct {
	// Code is the MGS#### identifier.
	Code string `json:"code"`
	// Message is the human-readable finding.
	Message string `json:"message,omitempty"`
	// URL points at the code's documentation page, when the producer knew it.
	URL string `json:"url,omitempty"`
	// Unit is "<project>:<target>", or a bare project path when the finding is
	// not target-scoped. It is the producer's own addressing and is NOT split
	// into Project and Target here: some diagnostics are raised before a target
	// is resolved, and inventing an empty Target would claim more than magus knows.
	Unit string `json:"unit,omitempty"`
	// File is the workspace-relative path the finding concerns, when there is one.
	// Line is 1-indexed and 0 means "no line", which is what an editor needs to
	// decide between a buffer annotation and a project-level message.
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitzero"`
}

// StreamType implements [StreamBody].
func (StreamDiagnosticBody) StreamType() StreamEventType { return StreamDiagnostic }

// StreamChange is the body of [StreamWorkspaceChanged]: source files that moved
// on disk.
//
// Paths are batched rather than sent one per event because an editor save, a
// branch switch, and a generator run all produce bursts, and a subscriber that
// rebuilds per path would do the work N times.
type StreamChange struct {
	// Paths are workspace-relative, matching `magus watch` output so the two
	// surfaces agree.
	Paths []string `json:"paths"`
}

// StreamType implements [StreamBody].
func (StreamChange) StreamType() StreamEventType { return StreamWorkspaceChanged }

// StreamAttention is the body of [StreamAttentionRaised]: the human-facing
// [Event] forwarded onto the machine stream.
//
// It embeds the whole Event rather than flattening the interesting fields,
// because the attention record is a shipped contract with its own schema version
// and a renderer already reasons over its axes. Flattening would fork it.
type StreamAttention struct {
	// ID addresses the request for `magus session dispose`.
	ID string `json:"id"`
	// Request is the attention record as raised.
	Request Event `json:"request"`
}

// StreamType implements [StreamBody].
func (StreamAttention) StreamType() StreamEventType { return StreamAttentionRaised }

// StreamGuard is the body of [StreamGuardVerdict]: a decision the guard has
// already made.
//
// It reports, it does not ask. The verdict reached the agent host through
// `magus session hook`'s reply before this event was emitted, and nothing a
// subscriber does can revisit it.
type StreamGuard struct {
	// Verdict is "deny", "advise", or "pass".
	Verdict string `json:"verdict"`
	// Surface is what was judged: "command" or "path".
	Surface string `json:"surface"`
	// Subject is the command line or file path the verdict was about.
	Subject string `json:"subject,omitempty"`
	// Reason is the explanation the guard gave, the same text the host relayed.
	Reason string `json:"reason,omitempty"`
	// Agent names the host that asked, when it sent one. Empty is normal: not
	// every host has a name to send, and an absent agent is missing attribution
	// rather than a missing verdict.
	Agent string `json:"agent,omitempty"`
}

// StreamType implements [StreamBody].
func (StreamGuard) StreamType() StreamEventType { return StreamGuardVerdict }

// streamHead is the envelope half of a marshalled line. It exists so
// [StreamEvent.MarshalJSON] can encode the fixed fields with the standard
// marshaller and splice the body beside them, rather than hand-writing JSON.
type streamHead struct {
	Schema    int             `json:"schema"`
	Type      StreamEventType `json:"type"`
	Ts        int64           `json:"ts"`
	Workspace string          `json:"workspace,omitempty"`
	Inv       string          `json:"inv,omitempty"`
}

// MarshalJSON encodes the event as one flat JSON object: the envelope fields
// followed by the body's own fields at the same level.
//
// It fails rather than emitting a partial line when Body is nil or marshals to
// anything but an object. A subscriber cannot recover from a typeless or
// malformed line, so producing one would push the failure somewhere it cannot be
// diagnosed.
func (e StreamEvent) MarshalJSON() ([]byte, error) {
	if e.Body == nil {
		return nil, fmt.Errorf("types: StreamEvent has no body")
	}
	head, err := json.Marshal(streamHead{
		Schema:    StreamSchema,
		Type:      e.Body.StreamType(),
		Ts:        e.Ts,
		Workspace: e.Workspace,
		Inv:       e.Inv,
	})
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(e.Body)
	if err != nil {
		return nil, fmt.Errorf("types: StreamEvent body %T: %w", e.Body, err)
	}
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return nil, fmt.Errorf("types: StreamEvent body %T marshals to %q (want a JSON object)", e.Body, body)
	}
	var buf bytes.Buffer
	buf.Grow(len(head) + len(body))
	buf.Write(head[:len(head)-1])
	if len(body) > 2 {
		buf.WriteByte(',')
		buf.Write(body[1 : len(body)-1])
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// DecodeStreamEvent parses one wire line back into a [StreamEvent].
//
// It is the counterpart to [StreamEvent.MarshalJSON] for Go subscribers and for
// round-trip tests; the reference clients decode in their own language. An
// unrecognized type is an error here rather than a skip, because a Go caller can
// check [StreamEventType] before decoding and a silent zero body would be worse
// than a refusal. Line-oriented consumers that want the documented
// skip-what-you-do-not-know behavior should switch on the type first.
//
// A schema newer than [StreamSchema] is decoded rather than refused: the envelope
// contract is that additive changes keep old fields meaning what they meant, so a
// client built against schema 1 reads a schema 2 line correctly for the fields it
// knows.
func DecodeStreamEvent(line []byte) (StreamEvent, error) {
	var head streamHead
	if err := json.Unmarshal(line, &head); err != nil {
		return StreamEvent{}, fmt.Errorf("types: decode stream event: %w", err)
	}
	var body StreamBody
	switch head.Type {
	case StreamRunStarted, StreamRunFinished:
		var b StreamRun
		if err := json.Unmarshal(line, &b); err != nil {
			return StreamEvent{}, err
		}
		body = b
	case StreamTargetResult:
		var b StreamTarget
		if err := json.Unmarshal(line, &b); err != nil {
			return StreamEvent{}, err
		}
		body = b
	case StreamTargetOutput:
		var b StreamOutput
		if err := json.Unmarshal(line, &b); err != nil {
			return StreamEvent{}, err
		}
		body = b
	case StreamDiagnostic:
		var b StreamDiagnosticBody
		if err := json.Unmarshal(line, &b); err != nil {
			return StreamEvent{}, err
		}
		body = b
	case StreamWorkspaceChanged:
		var b StreamChange
		if err := json.Unmarshal(line, &b); err != nil {
			return StreamEvent{}, err
		}
		body = b
	case StreamAttentionRaised:
		var b StreamAttention
		if err := json.Unmarshal(line, &b); err != nil {
			return StreamEvent{}, err
		}
		body = b
	case StreamGuardVerdict:
		var b StreamGuard
		if err := json.Unmarshal(line, &b); err != nil {
			return StreamEvent{}, err
		}
		body = b
	default:
		return StreamEvent{}, fmt.Errorf("types: decode stream event: unknown type %q", head.Type)
	}
	return StreamEvent{Ts: head.Ts, Workspace: head.Workspace, Inv: head.Inv, Body: body}, nil
}

// StreamEventTypes lists the taxonomy in wire order for `magus events --help`,
// for flag validation, and for the drift gate that fails when a type is added
// without being documented.
//
// It is a function returning a fresh slice rather than an exported slice var so a
// caller cannot reorder or truncate the canonical list for everyone else.
func StreamEventTypes() []StreamEventType {
	return []StreamEventType{
		StreamRunStarted,
		StreamRunFinished,
		StreamTargetResult,
		StreamTargetOutput,
		StreamDiagnostic,
		StreamWorkspaceChanged,
		StreamAttentionRaised,
		StreamGuardVerdict,
	}
}

// StreamFilter selects which types a subscriber receives.
//
// The zero value is not "everything": it is everything EXCEPT
// [StreamTargetOutput], because that one type outnumbers all the others together
// on any real build and a subscriber that gets it by default drowns on its first
// `affected ci`. Asking for it is one flag; recovering from having been sent it
// unasked is a rewrite of the client.
type StreamFilter struct {
	// Types restricts the stream to these types. Empty means the default set
	// described above.
	Types []StreamEventType
}

// Allows reports whether t passes the filter.
func (f StreamFilter) Allows(t StreamEventType) bool {
	if len(f.Types) == 0 {
		return t != StreamTargetOutput
	}
	for _, want := range f.Types {
		if want == t {
			return true
		}
	}
	return false
}

// ParseStreamEventType validates a type name from a flag or a subscribe frame.
//
// It refuses an unknown name instead of ignoring it: a typo in `--type
// target.reslut` would otherwise present as a stream that is simply quiet, which
// is the failure a person debugs for an hour.
func ParseStreamEventType(s string) (StreamEventType, error) {
	for _, t := range StreamEventTypes() {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("types: unknown event type %q", s)
}
