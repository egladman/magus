// Package journal captures one magus invocation as a structured stream of events, the
// journal a run produces. Events are emitted through the standard library's slog: an
// invocation runs under a capture *slog.Logger threaded on ctx, whose handlers persist the
// JSONL run log and fan the stream out to any live viewers. Using slog.Handler as the
// transport (rather than a bespoke sink) keeps capture composable with the wider slog
// ecosystem, including an OpenTelemetry logs bridge, since a slog.Record maps onto an OTel
// LogRecord (message->Body, attrs->Attributes, our kind->EventName).
//
// The typed [Event] is the schema: it is what the JSONL store persists and what
// internal/handler maps onto the magus.viewer.v1alpha1 wire contract. This package is a leaf,
// compiled into the default binary: every run captures. Its only magus dependency is
// internal/json, the workspace's single codec, which reaches nothing itself.
package journal

import (
	"strconv"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/json"
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
	// KindSecret records that a credential was READ, the reference and the provider
	// that served it, never the value. A secret read is an auditable act: it is the
	// moment a build reached for something privileged, and "which references did this
	// run touch, through which backend" is the question an audit answers. The value is
	// deliberately absent, and journal.Emit redacts Text anyway, so a future edit that
	// tries to include one is caught rather than trusted.
	KindSecret = "secret"
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

// Event is one line of a structured invocation log, the atom of the stream. It serializes
// to a single compact JSON object (one JSONL line); empty fields are omitted so output
// lines stay small.
type Event struct {
	Ts         int64  `json:"ts"`                    // unix milliseconds
	Inv        string `json:"inv,omitempty"`         // invocation id (one per run command)
	Project    string `json:"project,omitempty"`     // repo-relative project path
	Target     string `json:"target,omitempty"`      // target name (with charms, as the CLI spells it)
	Kind       string `json:"kind"`                  // one of the Kind* constants
	Stream     string `json:"stream,omitempty"`      // stdout|stderr, for output events
	Level      string `json:"level,omitempty"`       // info|warn|error, for magus events
	Status     string `json:"status,omitempty"`      // pass|fail|cached, for result events
	Ref        string `json:"ref,omitempty"`         // target-output ref, for result events
	DurationMs int64  `json:"duration_ms,omitempty"` // wall-clock duration, for result events
	// CacheKey is the digest of everything the step's cache key was computed from, on
	// result events only. It is what makes "this target never replays" answerable:
	// repeated runs under DIFFERENT keys are the cache working, because the inputs moved,
	// and only a key that repeats without ever being replayed is evidence of a footprint
	// keyed on more than the target reads. Without it a reader counts runs and cannot
	// tell those apart.
	//
	// A HASH, never a counter. Two values are equal or not; neither is "newer", and
	// nothing may order them. Omitted on journals written before this field existed, and
	// the yield check degrades rather than guessing when it is absent.
	CacheKey string `json:"cache_key,omitempty"`
	Text     string `json:"text,omitempty"` // output line or message

	// Set ONLY on the started event (Kind==KindStarted): the run's identity, carried in the
	// stream itself so both the durable file and any live watcher learn which command
	// produced the run from its first frame. Omitted on every other event.
	Command      *Command `json:"command,omitempty"`
	MagusVersion string   `json:"magus_version,omitempty"`

	// Set ONLY on a scope event (Kind==KindScope) that carries no target: the projects
	// this run selected on files nothing declares. It rides the run's own stream rather
	// than a second channel because it is a fact ABOUT this run's scope, and because the
	// consumers that need it (a live viewer, the daemon's run registry) are already
	// reading these frames.
	Undeclared []UndeclaredSeed `json:"undeclared,omitempty"`
}

// UnmarshalJSON decodes an event, reading a duration written under either spelling.
//
// compat(until: no journal under <cacheDir>/runs still carries "dur_ms"): the duration was
// spelled `dur_ms` before [Event.DurationMs] was. Journals are append-only and rotate, so
// observing that dropping this is safe means finding none left:
//
//	grep -l '"dur_ms"' <cacheDir>/runs/*.jsonl
//
// Dropping it early is silent rather than loud: those durations read as zero, so a run row in
// the console and the viewer reports a minute of work as instant, and the cache-yield check
// (MGS1009) stops reporting targets it should have caught instead of failing.
func (e *Event) UnmarshalJSON(b []byte) error {
	type event Event // no method set, so this does not recurse
	var decoded event
	if err := json.Unmarshal(b, &decoded); err != nil {
		return err
	}
	*e = Event(decoded)
	if e.DurationMs == 0 {
		var legacy struct {
			DurMs int64 `json:"dur_ms"`
		}
		if json.Unmarshal(b, &legacy) == nil {
			e.DurationMs = legacy.DurMs
		}
	}
	return nil
}

// UndeclaredSeed is one project the affected set selected on changed files that no
// project declares (MGS1028), with the files that did the selecting.
//
// Inputs is the half that matters to a reader. An undeclared LICENSE costs a rerun
// whose answer could not have differed; an undeclared rule set or toolchain pin can
// leave a verdict computed under the OLD rules valid in the cache, which is a
// different claim about what the workspace can be trusted to have checked. Which
// files fall in it is types.LooksLikeBuildInput's answer, resolved by the emitter
// because this package is a stdlib-only leaf.
type UndeclaredSeed struct {
	Project string   `json:"project"`
	Files   []string `json:"files,omitempty"`
	Inputs  []string `json:"inputs,omitempty"`
}

// invSeq makes minted invocation ids unique within a process without a uuid dep.
var invSeq atomic.Uint64

// NewInvocationID mints a short, process-unique invocation id (time + counter). It is
// opaque, used only to group and address one run command's events.
func NewInvocationID() string {
	return "inv" + strconv.FormatInt(nowMillis(), 36) + strconv.FormatUint(invSeq.Add(1), 36)
}

// nowMillis is the one wall-clock read in this package (unix milliseconds).
func nowMillis() int64 { return time.Now().UnixMilli() }
