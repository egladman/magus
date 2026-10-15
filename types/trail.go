package types

import (
	"fmt"
	"strings"
)

// TrailSection is which part of a session's guard trail a row belongs to. The
// letter is the row's label prefix, so a person writes C3 for the third unguarded shape.
type TrailSection string

const (
	// TrailRefused rows are rules that denied a call, keyed by rule.
	TrailRefused TrailSection = "refused"
	// TrailAdvised rows are rules that let a call through with advice, keyed by rule.
	TrailAdvised TrailSection = "advised"
	// TrailUnguarded rows are command shapes that passed with no rule, keyed by shape:
	// the guard candidates.
	TrailUnguarded TrailSection = "unguarded"
	// TrailNextNotTaken rows are commands a verdict served as `next` that the agent
	// never ran, keyed by rule and the served command's shape.
	TrailNextNotTaken TrailSection = "next-not-taken"
)

// TrailSections lists every section in label order: A, B, C, D.
var TrailSections = []TrailSection{TrailRefused, TrailAdvised, TrailUnguarded, TrailNextNotTaken}

// Letter is the section's label prefix, "" for a value outside the set.
func (s TrailSection) Letter() string {
	for i, known := range TrailSections {
		if s == known {
			return string(rune('A' + i))
		}
	}
	return ""
}

// Validate reports a section outside the set, naming the ones that exist.
func (s TrailSection) Validate() error {
	if s.Letter() == "" {
		return fmt.Errorf("trail section %q is not one of %s", s, joinTrail(TrailSections))
	}
	return nil
}

// TrailVerdict is what a person said about a trail row.
type TrailVerdict string

const (
	// TrailShouldDeny says the row's calls should be refused next time.
	TrailShouldDeny TrailVerdict = "should-deny"
	// TrailShouldAdvise says the row's calls should draw advice next time.
	TrailShouldAdvise TrailVerdict = "should-advise"
	// TrailWrongDeny says the rule refused calls it should have let through.
	TrailWrongDeny TrailVerdict = "wrong-deny"
	// TrailFine says the row needs no change.
	TrailFine TrailVerdict = "fine"
)

// TrailVerdicts lists every verdict a mark may carry.
var TrailVerdicts = []TrailVerdict{TrailShouldDeny, TrailShouldAdvise, TrailWrongDeny, TrailFine}

// Validate reports a verdict outside the set, naming the ones that exist.
func (v TrailVerdict) Validate() error {
	for _, known := range TrailVerdicts {
		if v == known {
			return nil
		}
	}
	return fmt.Errorf("trail verdict %q is not one of %s", v, joinTrail(TrailVerdicts))
}

func joinTrail[T ~string](values []T) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}

// Trail is what the guard recorded about one session inside a window, as
// magus\trail.read returns it: every call it judged and every subagent the session
// started.
type Trail struct {
	// Session is the host's session id the observations belong to.
	Session string
	// Host is the agent host that reported the session, empty when its hooks named none.
	Host string
	// Start and End bound the window in unix milliseconds, End inclusive. Not Since and
	// Until: until is a Buzz keyword, and a field named by one reads as @"until".
	Start int64
	End   int64
	// Checkouts are the checkouts whose trails were read: every checkout of the
	// repository whose trail changed inside the window, since a session's hooks record
	// in whichever checkout they ran from.
	Checkouts []string
	// Observations are oldest first.
	Observations []TrailObservation
	// Spawns are the subagents the session started, oldest first; continuations are left
	// out.
	Spawns []TrailSpawn
}

// TrailReadOptions picks the session and window a trail read covers, the opts
// magus\trail.read takes. Every field is text as a person writes it, and a value that does
// not parse is an error, never the default.
type TrailReadOptions struct {
	// Session is the host's session id; empty reads the most recent session in the window.
	Session string `json:"session,omitempty"`
	// Since starts the window: a positive duration before Until ("6h") or an RFC3339
	// time. Empty is 24 hours before Until.
	Since string `json:"since,omitempty"`
	// Until ends the window, an RFC3339 time. Empty is now.
	Until string `json:"until,omitempty"`
}

// TrailObservation is one tool call the guard saw, and what it decided.
type TrailObservation struct {
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
	// Decision is pass, deny, advise or ask; empty for a read, which no rule judges.
	Decision string
	// Rule is the catalogued rule behind a deny, advise or ask; empty for a pass.
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

// TrailSpawn is one subagent a session started.
type TrailSpawn struct {
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

// TrailMark is a person's verdict on one trail row, kept per repository so every
// checkout and every later session sees it.
type TrailMark struct {
	// ID is the row's stable id: a digest of its section and key, so a mark outlives the
	// page position it was made from.
	ID string `json:"id"`
	// Section is which part of the trail the row came from.
	Section TrailSection `json:"section"`
	// Key is what the row groups by: a rule for refused and advised rows, a command shape
	// for unguarded ones, a rule and shape for next-not-taken ones.
	Key string `json:"key"`
	// Rule is the rule the row involves, empty for an unguarded shape.
	Rule string `json:"rule,omitempty"`
	// Verdict is what the person said.
	Verdict TrailVerdict `json:"verdict"`
	// Note is the person's own words, empty when they wrote none.
	Note string `json:"note,omitempty"`
	// Session is the session the row was read from.
	Session string `json:"session"`
	// At is unix milliseconds, stamped by the store.
	At int64 `json:"at"`
	// Examples are up to three commands the row held when it was marked.
	Examples []string `json:"examples,omitempty"`
}
