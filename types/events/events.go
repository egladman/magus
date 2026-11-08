package events

import (
	"bytes"
	"fmt"

	"github.com/egladman/magus/internal/json"
)

// Event is one fact about a workspace, addressed to a PROGRAM: the record
// an editor plugin, a status bar, or a notifier subscribes to. It is the single
// envelope magus's several internal producers are mapped onto, so an integrator
// learns one shape rather than five.
//
// It is the machine-facing sibling of [Event], and the split is by AUDIENCE, not
// by content. [Event] is what magus emits when it needs a PERSON: it carries an
// urgency tier and a situation class because a human renderer has to decide
// whether to interrupt someone. A Event carries neither, because a program
// subscribing to a build stream decides relevance by [EventType]. The two
// do not meet on the wire today: every mapped type is a run or target fact, and
// an attention request has no adapter onto this stream; see the const block.
//
// Nothing a subscriber does can change a magus verdict. The stream is outbound
// only, by design and not by omission: docs/scope.md seals the engine, the cache,
// the graph schema, and the guard's evaluation, and an extension seam "may change
// what magus does, never what a verdict means". There is deliberately no reply
// channel here. The inbound counterpart is `magus shell`, which is
// request/reply and whose verdict this stream can only report after the fact.
//
// Every field carries `json:"-"`: the wire shape comes from [Event.MarshalJSON]
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
// reading both streams should not meet two conventions. It also keeps a jq
// filter or an Emacs alist lookup one level deep.
type Event struct {
	// Ts is the event time in unix milliseconds, matching journal.Event.Ts so a
	// mapped event keeps the timestamp its producer recorded rather than the time
	// the mapping ran.
	Ts int64 `json:"-"`
	// Workspace is the absolute repository root. It is present on every event
	// because a subscriber may watch more than one workspace over a single
	// connection, and an event with no workspace cannot be routed to a buffer.
	Workspace string `json:"-"`
	// Inv groups every event belonging to one magus invocation. Empty for events
	// that belong to no run: a file change, an attention request, a guard verdict.
	Inv string `json:"-"`
	// Body carries the per-type fields and determines [Event.Type]. It is
	// never nil on a well-formed event; a nil Body marshals as an error rather
	// than as a typeless line.
	Body Body `json:"-"`
}

// Schema is the envelope version, stamped on every line as "schema".
//
// Bump it when a field is renamed or removed, or when an existing field changes
// meaning. Adding a new [EventType], or a new optional field to a body, is
// additive and does NOT bump it: a subscriber is required to ignore types and
// fields it does not recognize, which is what lets magus grow the taxonomy
// without breaking every client.
const Schema = 1

// EventType names the class of fact an event carries. A subscriber
// switches on it; an unrecognized value must be skipped, not treated as an error.
type EventType string

// The taxonomy, all four mapped from the run journal by internal/eventstream.
//
// Diagnostics, file changes, attention requests and guard verdicts have stores but no
// adapter, and are absent rather than silent: a type a subscriber can name and never
// receive is indistinguishable from a broken filter. Adding one is additive and does
// not bump [Schema]. See docs/guides/integrations/editor/design.md.
const (
	// RunStarted opens an invocation. Carries the command lineage, so a
	// subscriber can tell `magus run build` from `magus affected ci`.
	RunStarted EventType = "run.started"
	// RunFinished closes the invocation opened by RunStarted with the
	// same Inv. A subscriber that shows progress clears it here.
	RunFinished EventType = "run.finished"
	// TargetResult reports one target's outcome, cached replays included.
	// It carries the output ref, so a subscriber fetches the captured log on
	// demand instead of buffering every line it was sent.
	TargetResult EventType = "target.result"
	// TargetOutput is one line of a subprocess's stdout or stderr. It is the
	// only high-volume type in the taxonomy and is off unless a subscriber asks
	// for it; see [Filter].
	TargetOutput EventType = "target.output"
)

// Body is the per-type payload of a [Event]. Implementations are the
// Stream* body structs in this file; the interface is closed in practice, and
// [DecodeStreamEvent] fails on a type it does not know rather than guessing.
//
// Every implementation marshals to a JSON OBJECT. A body that marshals to an
// array or a scalar cannot be spliced flat into the envelope, and
// [Event.MarshalJSON] reports that as an error rather than emitting a line
// no subscriber can parse.
type Body interface {
	// StreamType reports the type stamped on the wire. It is derived from the body
	// rather than stored on the envelope so the two can never disagree.
	StreamType() EventType
}

// Run is the body of [RunStarted] and [RunFinished].
//
// Started and finished share one body because a subscriber correlates them by
// Inv and reads whichever fields the phase populates: Command and MagusVersion on
// the started event, Status and DurationMs on the finished one. Splitting them
// would double the wire types to save two optional fields.
type Run struct {
	// Phase is "started" or "finished".
	Phase string `json:"phase"`
	// Command is the full argument vector, subcommand included, as invoked.
	// Set on the started phase only.
	Command []string `json:"command,omitempty"`
	// Trigger is how the run was spawned: one of journal's Trigger constants
	// (run, affected, ci, x, watch, direct). Set on the started phase only.
	Trigger string `json:"trigger,omitempty"`
	// MagusVersion is the binary that produced the run. Set on the started phase.
	MagusVersion string `json:"magus_version,omitempty"`
	// Status is the overall outcome, "pass" or "fail". Set on the finished phase.
	// No duration: the journal's finished event carries none, so a subscriber
	// subtracts the started event's Ts.
	Status string `json:"status,omitempty"`
}

// StreamType reports [RunStarted] or [RunFinished] from Phase. An
// unset or unrecognized Phase reports RunFinished, so a malformed body
// still closes a run a subscriber has open rather than leaving a spinner forever.
func (b Run) StreamType() EventType {
	if b.Phase == "started" {
		return RunStarted
	}
	return RunFinished
}

// Target is the body of [TargetResult]: one target's outcome.
//
// It is emitted for cached targets too, which is what makes CacheHit meaningful:
// Status reports whether the target SUCCEEDED, CacheHit whether it actually ran.
// A subscriber that renders a replay as a fresh build is misreading the pair.
type Target struct {
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
	// line, which is why TargetOutput can stay opt-in.
	Ref string `json:"ref,omitempty"`
	// DurationMs is the target's wall-clock span in milliseconds.
	DurationMs int64 `json:"duration_ms,omitzero"`
	// Error is the failure message when Status is "failed".
	Error string `json:"error,omitempty"`
}

// StreamType implements [Body].
func (Target) StreamType() EventType { return TargetResult }

// Output is the body of [TargetOutput]: one line a subprocess wrote.
//
// This is the only type that scales with build size rather than with project
// count, and a busy `affected ci` emits tens of thousands. It is off unless a
// subscriber names it in a [Filter]; an editor that wants a full log should
// fetch it by ref from a [Target] instead of subscribing here.
type Output struct {
	Project string `json:"project,omitempty"`
	Target  string `json:"target,omitempty"`
	// Stream is "stdout" or "stderr".
	Stream string `json:"stream"`
	// Text is the line, without its trailing newline.
	Text string `json:"text"`
}

// StreamType implements [Body].
func (Output) StreamType() EventType { return TargetOutput }

// streamHead is the envelope half of a marshalled line. It exists so
// [Event.MarshalJSON] can encode the fixed fields with the standard
// marshaller and splice the body beside them, rather than hand-writing JSON.
type streamHead struct {
	Schema    int             `json:"schema"`
	Type      EventType `json:"type"`
	Ts        int64           `json:"ts"`
	Workspace string          `json:"workspace"`
	Inv       string          `json:"inv,omitempty"`
}

// MarshalJSON encodes the event as one flat JSON object: the envelope fields
// followed by the body's own fields at the same level.
//
// It fails rather than emitting a partial line when Body is nil or marshals to
// anything but an object. A subscriber cannot recover from a typeless or
// malformed line, so producing one would push the failure somewhere it cannot be
// diagnosed.
func (e Event) MarshalJSON() ([]byte, error) {
	if e.Body == nil {
		return nil, fmt.Errorf("types: Event has no body")
	}
	head, err := json.Marshal(streamHead{
		Schema:    Schema,
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
		return nil, fmt.Errorf("types: Event body %T: %w", e.Body, err)
	}
	if len(body) < 2 || body[0] != '{' || body[len(body)-1] != '}' {
		return nil, fmt.Errorf("types: Event body %T marshals to %q (want a JSON object)", e.Body, body)
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

// UnmarshalJSON decodes one wire line. Without it every field is tagged "-", so
// json.Unmarshal would report no error and leave a zero event with a nil Body.
func (e *Event) UnmarshalJSON(line []byte) error {
	got, err := DecodeStreamEvent(line)
	if err != nil {
		return err
	}
	*e = got
	return nil
}

// DecodeStreamEvent parses one wire line back into a [Event].
//
// It is the counterpart to [Event.MarshalJSON] for Go subscribers and for
// round-trip tests; the reference clients decode in their own language. An
// unrecognized type is an error here rather than a skip, because a Go caller can
// check [EventType] before decoding and a silent zero body would be worse
// than a refusal. Line-oriented consumers that want the documented
// skip-what-you-do-not-know behavior should switch on the type first.
//
// A schema newer than [Schema] is decoded rather than refused: the envelope
// contract is that additive changes keep old fields meaning what they meant, so a
// client built against schema 1 reads a schema 2 line correctly for the fields it
// knows.
func DecodeStreamEvent(line []byte) (Event, error) {
	var head streamHead
	if err := json.Unmarshal(line, &head); err != nil {
		return Event{}, fmt.Errorf("types: decode stream event: %w", err)
	}
	var body Body
	switch head.Type {
	case RunStarted, RunFinished:
		var b Run
		if err := json.Unmarshal(line, &b); err != nil {
			return Event{}, err
		}
		body = b
	case TargetResult:
		var b Target
		if err := json.Unmarshal(line, &b); err != nil {
			return Event{}, err
		}
		body = b
	case TargetOutput:
		var b Output
		if err := json.Unmarshal(line, &b); err != nil {
			return Event{}, err
		}
		body = b
	default:
		return Event{}, fmt.Errorf("types: decode stream event: unknown type %q", head.Type)
	}
	return Event{Ts: head.Ts, Workspace: head.Workspace, Inv: head.Inv, Body: body}, nil
}

// EventTypes lists the taxonomy in wire order for `magus events --help` and
// for flag validation. Every entry has a producer; see the const block.
//
// It is a function returning a fresh slice rather than an exported slice var so a
// caller cannot reorder or truncate the canonical list for everyone else.
func EventTypes() []EventType {
	return []EventType{
		RunStarted,
		RunFinished,
		TargetResult,
		TargetOutput,
	}
}

// Filter selects which types a subscriber receives.
//
// The zero value is not "everything": it is everything EXCEPT
// [TargetOutput], because that one type outnumbers all the others together
// on any real build and a subscriber that gets it by default drowns on its first
// `affected ci`. Asking for it is one flag; recovering from having been sent it
// unasked is a rewrite of the client.
type Filter struct {
	// Types restricts the stream to these types. Empty means the default set
	// described above.
	Types []EventType
}

// Allows reports whether t passes the filter.
func (f Filter) Allows(t EventType) bool {
	if len(f.Types) == 0 {
		return t != TargetOutput
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
func ParseStreamEventType(s string) (EventType, error) {
	for _, t := range EventTypes() {
		if string(t) == s {
			return t, nil
		}
	}
	return "", fmt.Errorf("types: unknown event type %q", s)
}
