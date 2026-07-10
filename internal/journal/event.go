// Package journal captures one magus invocation as a structured stream of events - the
// journal a run produces. Events are emitted through the standard library's slog: an
// invocation runs under a capture *slog.Logger threaded on ctx, whose handlers persist the
// JSONL run log and fan the stream out to any live viewers. Using slog.Handler as the
// transport (rather than a bespoke sink) keeps capture composable with the wider slog
// ecosystem - including an OpenTelemetry logs bridge, since a slog.Record maps onto an OTel
// LogRecord (message->Body, attrs->Attributes, our kind->EventName).
//
// The typed [Event] is the schema: it is what the JSONL store persists and what
// internal/handler maps onto the magus.viewer.v1 wire contract. This package is a leaf
// (stdlib-only), compiled into the default binary - every run captures.
package journal

import (
	"strconv"
	"sync/atomic"
	"time"
)

// Kind classifies an [Event]. Following OpenTelemetry's EventName idea, it names the class
// of event; the lifecycle pair brackets the content kinds.
const (
	// Lifecycle events bracket an invocation.
	KindStarted  = "started"  // invocation opens: command lineage + magus version
	KindFinished = "finished" // invocation closes: overall pass/fail outcome
	// Content events, produced between the lifecycle pair.
	KindExec   = "exec"   // a subprocess is about to run: the command line (groups the output below it)
	KindOutput = "output" // a subprocess stdout/stderr line
	KindResult = "result" // a target finished (pass/fail/cached), with its ref + duration
	KindScope  = "scope"  // the run's project scope header
	KindWarn   = "warn"   // a magus warning
)

// Stream values for [Event.Stream] on output events.
const (
	StreamStdout = "stdout"
	StreamStderr = "stderr"
)

// Status values for [Event.Status] on result events (and the overall outcome on finished).
const (
	StatusPass   = "pass"
	StatusFail   = "fail"
	StatusCached = "cached"
)

// Event is one line of a structured invocation log - the atom of the stream. It serializes
// to a single compact JSON object (one JSONL line); empty fields are omitted so output
// lines stay small.
type Event struct {
	Ts      int64  `json:"ts"`                // unix milliseconds
	Inv     string `json:"inv,omitempty"`     // invocation id (one per run command)
	Project string `json:"project,omitempty"` // repo-relative project path
	Target  string `json:"target,omitempty"`  // target name (with charms, as the CLI spells it)
	Kind    string `json:"kind"`              // one of the Kind* constants
	Stream  string `json:"stream,omitempty"`  // stdout|stderr, for output events
	Level   string `json:"level,omitempty"`   // info|warn|error, for magus events
	Status  string `json:"status,omitempty"`  // pass|fail|cached, for result events
	Ref     string `json:"ref,omitempty"`     // target-output ref, for result events
	DurMs   int64  `json:"dur_ms,omitempty"`  // duration in ms, for result events
	Text    string `json:"text,omitempty"`    // output line or message

	// Set ONLY on the started event (Kind==KindStarted): the run's identity, carried in the
	// stream itself so both the durable file and any live watcher learn which command
	// produced the run from its first frame. Omitted on every other event.
	Command      *Command `json:"command,omitempty"`
	MagusVersion string   `json:"magus_version,omitempty"`
}

// invSeq makes minted invocation ids unique within a process without a uuid dep.
var invSeq atomic.Uint64

// NewInvocationID mints a short, process-unique invocation id (time + counter). It is
// opaque - used only to group and address one run command's events.
func NewInvocationID() string {
	return "inv" + strconv.FormatInt(nowMillis(), 36) + strconv.FormatUint(invSeq.Add(1), 36)
}

// nowMillis is the one wall-clock read in this package (unix milliseconds).
func nowMillis() int64 { return time.Now().UnixMilli() }
