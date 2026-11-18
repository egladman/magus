package feedback

import (
	"fmt"
	"strings"
)

// Section is which part of a session's guard [Report] a row belongs to.
// Its letter prefixes the row's label: C3 is the third unguarded shape.
type Section string

const (
	// SectionRefused rows are rules that denied a call, keyed by rule.
	SectionRefused Section = "refused"
	// SectionAdvised rows are rules that let a call through with advice, keyed by rule.
	SectionAdvised Section = "advised"
	// SectionUnguarded rows are command shapes that passed with no rule, keyed by shape:
	// the guard candidates.
	SectionUnguarded Section = "unguarded"
	// SectionNextNotTaken rows are commands a verdict served as `next` that the agent
	// never ran, keyed by rule and the served command's shape.
	SectionNextNotTaken Section = "next-not-taken"
)

// Sections lists every section in label order: A, B, C, D.
func Sections() []Section {
	return []Section{SectionRefused, SectionAdvised, SectionUnguarded, SectionNextNotTaken}
}

// letter is the section's label prefix, "" for a value outside the set.
func (s Section) letter() string {
	for i, known := range Sections() {
		if s == known {
			return string(rune('A' + i))
		}
	}
	return ""
}

// Validate reports a section outside the set as an [*UnknownValueError].
func (s Section) Validate() error {
	if s.letter() == "" {
		return &UnknownValueError{Field: "section", Value: string(s), Known: valueStrings(Sections())}
	}
	return nil
}

// Verdict is what a person said about a report row.
type Verdict string

const (
	// VerdictShouldDeny says the row's calls should be refused next time.
	VerdictShouldDeny Verdict = "should-deny"
	// VerdictShouldAdvise says the row's calls should draw advice next time.
	VerdictShouldAdvise Verdict = "should-advise"
	// VerdictWrongDeny says the rule refused calls it should have let through.
	VerdictWrongDeny Verdict = "wrong-deny"
	// VerdictFine says the row needs no change.
	VerdictFine Verdict = "fine"
)

// Verdicts lists every verdict a mark may carry.
func Verdicts() []Verdict {
	return []Verdict{VerdictShouldDeny, VerdictShouldAdvise, VerdictWrongDeny, VerdictFine}
}

// Validate reports a verdict outside the set as an [*UnknownValueError].
func (v Verdict) Validate() error {
	for _, known := range Verdicts() {
		if v == known {
			return nil
		}
	}
	return &UnknownValueError{Field: "verdict", Value: string(v), Known: valueStrings(Verdicts())}
}

// UnknownValueError is a section or verdict outside its set. Known lists the set.
type UnknownValueError struct {
	Field string
	Value string
	Known []string
}

func (e *UnknownValueError) Error() string {
	return fmt.Sprintf("feedback: %s %q is not one of %s", e.Field, e.Value, strings.Join(e.Known, ", "))
}

func valueStrings[T ~string](values []T) []string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	return parts
}

// Report is what the guard recorded about one session inside a window.
// magus\session().trail() returns it: each call judged, each subagent started.
type Report struct {
	// Session is the host's session id the observations belong to.
	Session string
	// Host is the agent host that reported the session, empty when its hooks named none.
	Host string
	// Start and End bound the window in unix milliseconds, End inclusive. Not Since and
	// Until: until is a Buzz keyword, and a field named by one reads as @"until".
	Start int64
	End   int64
	// Checkouts are every checkout of the repository whose activity log
	// changed inside the window: a session's hooks record in whichever
	// checkout they ran from.
	Checkouts []string
	// Observations are oldest first.
	Observations []Observation
	// Spawns are the subagents the session started, oldest first; continuations are left
	// out.
	Spawns []Spawn
}

// ReadOptions picks the session and window a [Report] covers.
// magus\session(opts).trail() takes them.
// Each field is text a person writes; one that does not parse is an error.
type ReadOptions struct {
	// Session is the host's session id; empty reads the most recent session in the window.
	Session string `json:"session,omitempty"`
	// Since starts the window: a positive duration before Until ("6h") or an RFC3339
	// time. Empty is 24 hours before Until.
	Since string `json:"since,omitempty"`
	// Until ends the window, an RFC3339 time. Empty is now.
	Until string `json:"until,omitempty"`
}

// Observation is one tool call the guard saw, and what it decided.
type Observation struct {
	// At is unix milliseconds.
	At int64
	// Agent is the host's subagent id, empty for the main conversation.
	Agent string
	// Lease is the job the call was graded under, empty when none was bound.
	Lease string
	// Tool is shell.command, file.write or file.read.
	Tool string
	// Command is the shell line with credentials redacted; Path is the file of a write or
	// read. One of the two is set.
	Command string
	Path    string
	// Decision is defer, deny, advise or ask; empty for a read, which no rule judges.
	Decision string
	// Rule is the catalogued rule behind a deny, advise or ask; empty for a defer.
	Rule string
	// Nexts are the commands the verdict served under `next:`, in order.
	Nexts []string
	// PreauthorizedBy names the served next that let this exact command through, empty
	// for a command the guard graded.
	PreauthorizedBy string
	// Shape is Command with its paths, patterns and literals normalized away, so two
	// calls that differ only in what they name read alike; empty for a line the shell
	// parser cannot read, and for a write or read.
	Shape string
	// Shapes are the shapes of each program the line runs, in order, each with its own
	// redirections: what unguarded calls cluster by, since a whole line's shape is nearly
	// unique once a session chains commands. Empty where Shape is.
	Shapes []string
}

// Spawn is one subagent a session started.
type Spawn struct {
	// At is unix milliseconds.
	At int64
	// Agent is the spawning subagent's id, empty when the main conversation spawned it.
	Agent string
	// Child is the label the host gave the new subagent.
	Child string
	// Lease is the job the handed prompt named on its first line, empty when none.
	Lease string
	// Model is the model the spawning call declared for the child, empty when it named
	// none. A claim, never checked.
	Model string
}

// Mark is a person's verdict on one report row, kept per repository so
// every checkout and every later session sees it.
type Mark struct {
	// ID is the row's stable id: a digest of its section and key, so a mark outlives the
	// page position it was made from.
	ID string `json:"id"`
	// Section is which part of the report the row came from.
	Section Section `json:"section"`
	// Key is what the row groups by: a rule for refused and advised rows, a command shape
	// for unguarded ones, a rule and shape for next-not-taken ones.
	Key string `json:"key"`
	// Rule is the rule the row involves, empty for an unguarded shape.
	Rule string `json:"rule,omitempty"`
	// Verdict is what the person said.
	Verdict Verdict `json:"verdict"`
	// Note is the person's own words, empty when they wrote none.
	Note string `json:"note,omitempty"`
	// Session is the session the row was read from.
	Session string `json:"session"`
	// At is unix milliseconds, stamped by the store.
	At int64 `json:"at"`
	// Examples are up to three commands the row held when it was marked.
	Examples []string `json:"examples,omitempty"`
}
