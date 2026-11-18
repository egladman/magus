package journal

import (
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/egladman/magus/libs/codec"
)

// Kind names what a [Record] reports. The set grows without a schema change:
// a reader meets a kind it does not know as a Record whose accessors all
// report false.
type Kind string

const (
	KindStarted  Kind = "started"  // the invocation opens
	KindFinished Kind = "finished" // the invocation closes
	KindExec     Kind = "exec"     // a subprocess is about to run
	KindOutput   Kind = "output"   // one line a subprocess wrote
	KindRun      Kind = "result"   // one target finished; "result" on disk
	KindScope    Kind = "scope"    // what the invocation selected
	KindSecret   Kind = "secret"   // a credential read, never the value
)

func (k Kind) known() bool {
	switch k {
	case KindStarted, KindFinished, KindExec, KindOutput, KindRun, KindScope, KindSecret:
		return true
	}
	return false
}

// Status is how a run, or a whole invocation, ended.
type Status string

const (
	StatusPass   Status = "pass"
	StatusFail   Status = "fail"
	StatusCached Status = "cached" // replayed, not executed
	// StatusPermitted is green only because a deviation ledger permitted its
	// findings.
	StatusPermitted Status = "permitted"
)

// Sizing says where a run's memory claim came from.
type Sizing string

const (
	SizingDeclared Sizing = "declared"
	SizingMeasured Sizing = "measured"
	SizingNone     Sizing = "none"
)

// OutputStream is which of a subprocess's streams a line came from.
type OutputStream string

const (
	Stdout OutputStream = "stdout"
	Stderr OutputStream = "stderr"
)

// Trigger is how an invocation was started.
type Trigger string

const (
	TriggerRun      Trigger = "run"
	TriggerAffected Trigger = "affected"
	TriggerCI       Trigger = "ci"
	TriggerX        Trigger = "x"
	TriggerWatch    Trigger = "watch"
	TriggerDirect   Trigger = "direct"
)

// Record is one line of an invocation's journal. Time and Invocation are on
// every line; the rest belongs to the kind, read through the accessor of that
// name. The zero Record has kind "" and no body.
//
// A Record is a value: copy it, send it on a channel, compare its accessors'
// results.
type Record struct {
	Time       time.Time // "ts" on disk, unix milliseconds
	Invocation string    // "inv" on disk

	line line
	// raw is the line as read, kept only for a kind this build does not know.
	raw []byte
}

// line is the journal line as the writer lays it out. Its fields, tags and
// order are the on-disk format. A number carries no omitempty: v2 never
// omitted one, so every line on disk holds them, and v1 must write them too.
type line struct {
	Ts             int64            `json:"ts"`
	Inv            string           `json:"inv,omitempty"`
	Project        string           `json:"project,omitempty"`
	Target         string           `json:"target,omitempty"`
	Kind           Kind             `json:"kind"`
	Stream         OutputStream     `json:"stream,omitempty"`
	Level          string           `json:"level,omitempty"`
	Status         Status           `json:"status,omitempty"`
	Ref            string           `json:"ref,omitempty"`
	DurationMs     int64            `json:"duration_ms"`
	CacheKey       string           `json:"cache_key,omitempty"`
	Text           string           `json:"text,omitempty"`
	DiagnosticCode string           `json:"diagnostic_code,omitempty"`
	StartedMs      int64            `json:"started_ms"`
	Slots          int              `json:"slots"`
	MemoryMB       int              `json:"memory_mb"`
	Sizing         Sizing           `json:"sizing,omitempty"`
	WaitedMs       int64            `json:"waited_ms"`
	MaxRSSBytes    int64            `json:"max_rss_bytes"`
	CPUMs          int64            `json:"cpu_ms"`
	Command        *Command         `json:"command,omitzero"`
	MagusVersion   string           `json:"magus_version,omitempty"`
	Undeclared     []UndeclaredSeed `json:"undeclared,omitempty"`
}

// Body is the set of types a Record can carry, one per [Kind].
type Body interface {
	Started | Finished | Exec | Output | Run | Scope | Secret
}

// NewRecord returns the Record that carries body. It writes nothing: a Record
// reaches a store only through a writer, and this package has none. A test
// builds its fixtures with it.
func NewRecord[B Body](at time.Time, invocation string, body B) Record {
	r := Record{Time: at, Invocation: invocation}
	switch b := any(body).(type) {
	case Started:
		r.line.Kind = KindStarted
		if !b.Command.zero() {
			command := b.Command
			r.line.Command = &command
		}
		r.line.MagusVersion = b.Version
	case Finished:
		r.line.Kind = KindFinished
		r.line.Status = b.Status
	case Exec:
		r.line.Kind = KindExec
		r.line.Project, r.line.Target, r.line.Text = b.Project, b.Target, b.Command
	case Output:
		r.line.Kind = KindOutput
		r.line.Project, r.line.Target = b.Project, b.Target
		r.line.Stream, r.line.Text = b.Stream, b.Text
	case Run:
		r.line = line{
			Kind:           KindRun,
			Project:        b.Project,
			Target:         b.Target,
			Level:          b.Level,
			Status:         b.Status,
			Ref:            b.Ref,
			DurationMs:     b.Duration.Milliseconds(),
			CacheKey:       b.CacheKey,
			Text:           b.Error,
			DiagnosticCode: b.DiagnosticCode,
			StartedMs:      unixMilli(b.Started),
			Slots:          b.Slots,
			MemoryMB:       b.MemoryMB,
			Sizing:         b.Sizing,
			WaitedMs:       b.Waited.Milliseconds(),
			MaxRSSBytes:    b.MaxRSSBytes,
			CPUMs:          b.CPU.Milliseconds(),
		}
	case Scope:
		r.line.Kind = KindScope
		r.line.Undeclared = b.Undeclared
	case Secret:
		r.line.Kind = KindSecret
		r.line.Project, r.line.Target, r.line.Text = b.Project, b.Target, b.Text
	default:
		// Unreachable while Body lists one case per type above; the zero
		// Record it gives is one MarshalJSON refuses.
		return Record{}
	}
	return r
}

// Kind reports what r carries. It is a value this build may not know.
func (r Record) Kind() Kind { return r.line.Kind }

// Started returns r's body and true when r opens an invocation, and the zero
// body and false otherwise. Of the accessors, exactly one reports true for a
// kind this build knows, and none for one it does not.
func (r Record) Started() (Started, bool) {
	if r.line.Kind != KindStarted {
		return Started{}, false
	}
	b := Started{Version: r.line.MagusVersion}
	if r.line.Command != nil {
		b.Command = *r.line.Command
	}
	return b, true
}

// Finished returns r's body and true when r closes an invocation.
func (r Record) Finished() (Finished, bool) {
	if r.line.Kind != KindFinished {
		return Finished{}, false
	}
	return Finished{Status: r.line.Status}, true
}

// Exec returns r's body and true when r is a subprocess about to run.
func (r Record) Exec() (Exec, bool) {
	if r.line.Kind != KindExec {
		return Exec{}, false
	}
	return Exec{Project: r.line.Project, Target: r.line.Target, Command: r.line.Text}, true
}

// Output returns r's body and true when r is a line a subprocess wrote.
func (r Record) Output() (Output, bool) {
	if r.line.Kind != KindOutput {
		return Output{}, false
	}
	l := &r.line
	return Output{Project: l.Project, Target: l.Target, Stream: l.Stream, Text: l.Text}, true
}

// Run returns r's body and true when r is a target that finished.
func (r Record) Run() (Run, bool) {
	if r.line.Kind != KindRun {
		return Run{}, false
	}
	l := &r.line
	return Run{
		Project:        l.Project,
		Target:         l.Target,
		Level:          l.Level,
		Status:         l.Status,
		Ref:            l.Ref,
		Duration:       millis(l.DurationMs),
		CacheKey:       l.CacheKey,
		Error:          l.Text,
		DiagnosticCode: l.DiagnosticCode,
		Started:        fromUnixMilli(l.StartedMs),
		Slots:          l.Slots,
		MemoryMB:       l.MemoryMB,
		Sizing:         l.Sizing,
		Waited:         millis(l.WaitedMs),
		MaxRSSBytes:    l.MaxRSSBytes,
		CPU:            millis(l.CPUMs),
	}, true
}

// Scope returns r's body and true when r is what the invocation selected.
func (r Record) Scope() (Scope, bool) {
	if r.line.Kind != KindScope {
		return Scope{}, false
	}
	return Scope{Undeclared: slices.Clone(r.line.Undeclared)}, true
}

// Secret returns r's body and true when r records a credential read.
func (r Record) Secret() (Secret, bool) {
	if r.line.Kind != KindSecret {
		return Secret{}, false
	}
	return Secret{Project: r.line.Project, Target: r.line.Target, Text: r.line.Text}, true
}

// LogValue lets a host log r with its own logger. It is a group keyed as the
// line is on disk, with the time under "time". A kind this build does not know
// logs its kind, its invocation and the raw line under "raw".
func (r Record) LogValue() slog.Value {
	attrs := []slog.Attr{slog.Time("time", r.Time)}
	attrs = appendString(attrs, "inv", r.Invocation)
	if r.raw != nil {
		attrs = append(attrs, slog.String("kind", string(r.line.Kind)), slog.String("raw", string(r.raw)))
		return slog.GroupValue(attrs...)
	}
	l := &r.line
	attrs = appendString(attrs, "project", l.Project)
	attrs = appendString(attrs, "target", l.Target)
	attrs = append(attrs, slog.String("kind", string(l.Kind)))
	attrs = appendString(attrs, "stream", string(l.Stream))
	attrs = appendString(attrs, "level", l.Level)
	attrs = appendString(attrs, "status", string(l.Status))
	attrs = appendString(attrs, "ref", l.Ref)
	attrs = appendInt(attrs, "duration_ms", l.DurationMs)
	attrs = appendString(attrs, "cache_key", l.CacheKey)
	attrs = appendString(attrs, "text", l.Text)
	attrs = appendString(attrs, "diagnostic_code", l.DiagnosticCode)
	attrs = appendInt(attrs, "started_ms", l.StartedMs)
	attrs = appendInt(attrs, "slots", int64(l.Slots))
	attrs = appendInt(attrs, "memory_mb", int64(l.MemoryMB))
	attrs = appendString(attrs, "sizing", string(l.Sizing))
	attrs = appendInt(attrs, "waited_ms", l.WaitedMs)
	attrs = appendInt(attrs, "max_rss_bytes", l.MaxRSSBytes)
	attrs = appendInt(attrs, "cpu_ms", l.CPUMs)
	if l.Command != nil {
		attrs = append(attrs, slog.Group("command",
			slog.Any("arguments", l.Command.Arguments),
			slog.String("cwd", l.Command.Cwd),
			slog.String("trigger", string(l.Command.Trigger))))
	}
	attrs = appendString(attrs, "magus_version", l.MagusVersion)
	if len(l.Undeclared) > 0 {
		attrs = append(attrs, slog.Any("undeclared", l.Undeclared))
	}
	return slog.GroupValue(attrs...)
}

func appendString(attrs []slog.Attr, key, value string) []slog.Attr {
	if value == "" {
		return attrs
	}
	return append(attrs, slog.String(key, value))
}

func appendInt(attrs []slog.Attr, key string, value int64) []slog.Attr {
	if value == 0 {
		return attrs
	}
	return append(attrs, slog.Int64(key, value))
}

var errZeroRecord = errors.New("journal: the zero Record carries no kind to marshal")

// MarshalJSON writes r as its journal line: the same keys, in the same order,
// as the line it was read from. A Record of a kind this build does not know
// marshals to the line it was read from, byte for byte. The zero Record is an
// error.
func (r Record) MarshalJSON() ([]byte, error) {
	if r.raw != nil {
		return slices.Clone(r.raw), nil
	}
	if r.line.Kind == "" {
		return nil, errZeroRecord
	}
	l := r.line
	l.Ts = unixMilli(r.Time)
	l.Inv = r.Invocation
	return codec.Marshal(l)
}

// UnmarshalJSON reads one journal line. An unknown kind is not an error, and
// an unknown key is ignored.
func (r *Record) UnmarshalJSON(data []byte) error {
	var l line
	if err := codec.Unmarshal(data, &l); err != nil {
		return err
	}
	*r = Record{Time: fromUnixMilli(l.Ts), Invocation: l.Inv}
	if !l.Kind.known() {
		r.line.Kind = l.Kind
		r.raw = slices.Clone(data)
		return nil
	}
	l.Ts, l.Inv = 0, ""
	r.line = l
	return nil
}

// Started opens an invocation.
type Started struct {
	Command Command
	// Version is the producing binary's version; "magus_version" on disk.
	Version string
}

// Finished closes an invocation. A journal with no Finished was interrupted.
type Finished struct {
	Status Status
}

// Exec is a subprocess about to run under a target.
type Exec struct {
	Project string
	Target  string
	Command string // the command line; "text" on disk
}

// Output is one line a subprocess wrote, without its newline.
type Output struct {
	Project string
	Target  string
	Stream  OutputStream
	Text    string
}

// Run is one target finished under the invocation: executed, replayed or
// refused.
type Run struct {
	Project string
	Target  string
	Level   string // "info" or "error"
	Status  Status
	// Ref is the output reference; empty when the target never ran.
	Ref      string
	Duration time.Duration // "duration_ms" on disk
	// CacheKey is the digest the step's cache key was computed from. Two are
	// equal or not; neither is newer.
	CacheKey string
	Error    string // the failure text; "text" on disk
	// DiagnosticCode is the code behind a run that never executed, which has
	// no Ref to read the cause from.
	DiagnosticCode string
	// What the run claimed and cost; each is zero when not recorded.
	Started     time.Time // when it was admitted; "started_ms"
	Slots       int
	MemoryMB    int
	Sizing      Sizing
	Waited      time.Duration // queued for its claim; "waited_ms"
	MaxRSSBytes int64
	CPU         time.Duration // "cpu_ms"
}

// Scope is what the invocation selected on files no project declares.
type Scope struct {
	Undeclared []UndeclaredSeed
}

// UndeclaredSeed is one project selected on changed files that no project
// declares. Inputs are the Files that look like build inputs.
type UndeclaredSeed struct {
	Project string   `json:"project"`
	Files   []string `json:"files,omitempty"`
	Inputs  []string `json:"inputs,omitempty"`
}

// Secret records that a credential was read under a target.
type Secret struct {
	Project string
	Target  string
	Text    string // which reference, through which provider
}

// Command is the command line an invocation was asked to run.
type Command struct {
	Arguments []string `json:"arguments,omitempty"`
	Cwd       string   `json:"cwd,omitempty"`
	Trigger   Trigger  `json:"trigger,omitempty"`
}

func (c Command) zero() bool {
	return len(c.Arguments) == 0 && c.Cwd == "" && c.Trigger == ""
}

// Invocation is one command, launch to exit, as its journal's Started and
// Finished records tell it. It is derived, never stored, and has no JSON form
// here.
type Invocation struct {
	ID      string
	Command Command
	// Started is zero when the journal's first line is not a Started record.
	Started time.Time
	// Finished is the last record's time when the journal has no Finished
	// record.
	Finished time.Time
	Status   Status // empty when the journal has no Finished record
	Version  string
}

// unixMilli is 0 for the zero time, so a line written without a time reads
// back without one.
func unixMilli(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

func fromUnixMilli(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms)
}

func millis(ms int64) time.Duration { return time.Duration(ms) * time.Millisecond }
