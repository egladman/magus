package types

// Event is the canonical record something emits when it needs a human's
// attention. It is shared across magus surfaces - the notify CLI, the activity
// trail, doctor reports, self-update notices, hints - so every consumer
// reasons over the same shape.
//
// The fields are categorical, one piece of information each:
//
//   - Outcome: the SITUATION class (waiting, permission, failed, ...)
//   - Severity: the URGENCY tier (info, notice, warning, critical)
//   - Source: who/what produced this (agent/claude-code, magus/doctor, ...)
//   - Where: actionable context (workspace, project, files)
//   - Message: the free-form detail
//
// The categorical split mirrors HTTP status classes (1xx/2xx/3xx/4xx/5xx)
// and Unix exit codes (0/1/2/126/127/128+N): one field carries the high-level
// class, another carries the specific code within it. The renderer can
// reason over each axis independently - title from outcome, icon from
// severity, deep-link from source+where.
//
// logger levels are deliberately NOT a part of this type. slog levels filter
// output (verbosity gates); event severities drive renderer behavior (icon,
// priority, action). Same names overlap (info, warning) by coincidence, but the
// vocabularies serve different purposes. When a call site needs to bridge
// (a log entry becomes an event), the mapping lives inline at that site.
type Event struct {
	SchemaVersion int            `json:"schema_version"`
	Outcome       EventOutcome   `json:"outcome"`
	Severity      EventSeverity  `json:"severity"`
	Source        EventSource    `json:"source"`
	Where         *EventLocation `json:"where,omitempty"`
	Message       string         `json:"message"`
}

// EventSchemaVersion is the current envelope version. Bump on breaking
// changes; additive optional fields do not require a bump.
const EventSchemaVersion = 1

// EventOutcome names the situation class - WHAT KIND of attention is needed.
// Seven canonical values; an unrecognised outcome falls through to other.
type EventOutcome string

const (
	// OutcomeWaiting: blocked on input (agent idle, interactive prompt).
	OutcomeWaiting EventOutcome = "waiting"
	// OutcomePermission: blocked on approval (gated operation, host permission prompt).
	OutcomePermission EventOutcome = "permission"
	// OutcomeFailed: exited non-zero or errored (run, affected ci, spell op).
	OutcomeFailed EventOutcome = "failed"
	// OutcomeFinished: completed successfully (rare for notifications; success is usually silent).
	OutcomeFinished EventOutcome = "finished"
	// OutcomeDiagnostic: a workspace problem was detected (doctor, merge-driver conflict).
	OutcomeDiagnostic EventOutcome = "diagnostic"
	// OutcomeUpdate: a new release or upgrade is available (self update).
	OutcomeUpdate EventOutcome = "update"
	// OutcomeOther: catch-all for situations outside the canonical set.
	OutcomeOther EventOutcome = "other"
)

// EventSeverity names the urgency tier - HOW URGENT is the attention.
// Four canonical tiers; aligned by name with slog's info/warning (those
// overlap intentionally) and adding notice (between info and warning) and
// critical (above error) which have no slog equivalent.
type EventSeverity string

const (
	// SeverityInfo: FYI, non-urgent. Same word as slog.LevelInfo; different intent.
	SeverityInfo EventSeverity = "info"
	// SeverityNotice: saw something, not actionable yet. No slog equivalent.
	SeverityNotice EventSeverity = "notice"
	// SeverityWarning: action likely needed. Same word as slog.LevelWarn; different intent.
	SeverityWarning EventSeverity = "warning"
	// SeverityCritical: action required. Above slog.LevelError in urgency.
	SeverityCritical EventSeverity = "critical"
)

// EventSource identifies who or what produced the event. Kind names the broad
// producer category; Sub names the producer's local component. ID is opaque
// instance identity (a session id, a run id, a process id) when available.
type EventSource struct {
	// Kind is the producer category. Producers set this; consumers match on it.
	Kind string `json:"kind"`
	// Sub is the component within Kind. Empty when there is no meaningful component.
	Sub string `json:"sub,omitempty"`
	// ID: opaque instance identity (session id, run id, process id). Empty
	// when the producer does not have one.
	ID string `json:"id,omitempty"`
}

// EventLocation is the actionable context - WHERE did this happen? Every
// field is optional; the renderer uses what is present.
//
// Workspace is the repo root (always a directory). Project names the
// workspace-relative project the event pertains to, when narrower than the
// whole workspace. Files names the relevant files - "the file X had a
// problem" reads differently from "the directory Y had a problem", and
// Path.IsDir carries that distinction.
type EventLocation struct {
	Workspace Path        `json:"workspace"`
	Project   *ProjectRef `json:"project,omitempty"`
	Files     []FileRef   `json:"files,omitempty"`
}

// FileRef is one file path with its kind. Mirrors Path but lives on the
// event record so the encoder does not need to reach into the types.Path
// API for every consumer; Path.Value + Path.IsDir flattened to two named
// fields reads more clearly at the call site.
//
// When a renderer wants to display "the file X had a problem" vs
// "the directory Y had a problem", FileRef.IsDir tells it which.
type FileRef struct {
	Value string `json:"value"`
	IsDir bool   `json:"is_dir,omitempty"`
}

// PathFromRef builds a types.Path from a FileRef, so consumers that already
// work with Path can move between the two without a helper at every site.
//
// Field-by-field, not a struct conversion. The two types happened to share a layout, so
// Path(r) compiled - and that made every future field on Path a silent compile break
// here, which is exactly what adding Path.Base did. A FileRef is an event payload naming
// a file; a Path is a lexical reference measured from a base. They are not the same idea
// and should not be coupled by their field order.
//
// Base is empty: a FileRef carries no base, and inventing one (the workspace root, say)
// would assert something the event never said.
func (r FileRef) PathFromRef() Path {
	return Path{Value: r.Value, IsDir: r.IsDir}
}

// FileRefFromPath is the inverse of PathFromRef, for producers that have a
// types.Path in hand and need to emit a FileRef.
//
// Resolve first: a FileRef has nowhere to put a base, so emitting p.Value raw would ship
// a relative path stripped of what it was relative to - readable, and wrong.
func FileRefFromPath(p Path) FileRef {
	abs := p.Resolve()
	return FileRef{Value: abs.Value, IsDir: abs.IsDir}
}
