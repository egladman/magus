package types

import (
	"fmt"
	"strings"
)

// FeedbackSection is which part of a session's guard feedback a row belongs to. The
// letter is the row's label prefix, so a person writes C3 for the third unguarded shape.
type FeedbackSection string

const (
	// FeedbackRefused rows are rules that denied a call, keyed by rule.
	FeedbackRefused FeedbackSection = "refused"
	// FeedbackAdvised rows are rules that let a call through with advice, keyed by rule.
	FeedbackAdvised FeedbackSection = "advised"
	// FeedbackUnguarded rows are command shapes that passed with no rule, keyed by shape:
	// the guard candidates.
	FeedbackUnguarded FeedbackSection = "unguarded"
	// FeedbackNextNotTaken rows are commands a verdict served as `next` that the agent
	// never ran, keyed by rule and the served command's shape.
	FeedbackNextNotTaken FeedbackSection = "next-not-taken"
)

// FeedbackSections lists every section in label order: A, B, C, D.
var FeedbackSections = []FeedbackSection{FeedbackRefused, FeedbackAdvised, FeedbackUnguarded, FeedbackNextNotTaken}

// Letter is the section's label prefix, "" for a value outside the set.
func (s FeedbackSection) Letter() string {
	for i, known := range FeedbackSections {
		if s == known {
			return string(rune('A' + i))
		}
	}
	return ""
}

// Validate reports a section outside the set, naming the ones that exist.
func (s FeedbackSection) Validate() error {
	if s.Letter() == "" {
		return fmt.Errorf("feedback section %q is not one of %s", s, joinFeedback(FeedbackSections))
	}
	return nil
}

// FeedbackVerdict is what a person said about a feedback row.
type FeedbackVerdict string

const (
	// FeedbackShouldDeny says the row's calls should be refused next time.
	FeedbackShouldDeny FeedbackVerdict = "should-deny"
	// FeedbackShouldAdvise says the row's calls should draw advice next time.
	FeedbackShouldAdvise FeedbackVerdict = "should-advise"
	// FeedbackWrongDeny says the rule refused calls it should have let through.
	FeedbackWrongDeny FeedbackVerdict = "wrong-deny"
	// FeedbackFine says the row needs no change.
	FeedbackFine FeedbackVerdict = "fine"
)

// FeedbackVerdicts lists every verdict a mark may carry.
var FeedbackVerdicts = []FeedbackVerdict{FeedbackShouldDeny, FeedbackShouldAdvise, FeedbackWrongDeny, FeedbackFine}

// Validate reports a verdict outside the set, naming the ones that exist.
func (v FeedbackVerdict) Validate() error {
	for _, known := range FeedbackVerdicts {
		if v == known {
			return nil
		}
	}
	return fmt.Errorf("feedback verdict %q is not one of %s", v, joinFeedback(FeedbackVerdicts))
}

func joinFeedback[T ~string](values []T) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = string(v)
	}
	return strings.Join(parts, ", ")
}

// FeedbackTrail is what the guard recorded about one session inside a window, as
// magus\trail.read returns it: every call it judged and every subagent the session
// started.
type FeedbackTrail struct {
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
	Observations []FeedbackObservation
	// Spawns are the subagents the session started, oldest first; continuations are left
	// out.
	Spawns []FeedbackSpawn
}

// FeedbackObservation is one tool call the guard saw, and what it decided.
type FeedbackObservation struct {
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

// FeedbackSpawn is one subagent a session started.
type FeedbackSpawn struct {
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

// FeedbackMark is a person's verdict on one feedback row, kept per repository so every
// checkout and every later session sees it.
type FeedbackMark struct {
	// ID is the row's stable id: a digest of its section and key, so a mark outlives the
	// page position it was made from.
	ID string `json:"id"`
	// Section is which part of the feedback the row came from.
	Section FeedbackSection `json:"section"`
	// Key is what the row groups by: a rule for refused and advised rows, a command shape
	// for unguarded ones, a rule and shape for next-not-taken ones.
	Key string `json:"key"`
	// Rule is the rule the row involves, empty for an unguarded shape.
	Rule string `json:"rule,omitempty"`
	// Verdict is what the person said.
	Verdict FeedbackVerdict `json:"verdict"`
	// Note is the person's own words, empty when they wrote none.
	Note string `json:"note,omitempty"`
	// Session is the session the row was read from.
	Session string `json:"session"`
	// At is unix milliseconds, stamped by the store.
	At int64 `json:"at"`
	// Examples are up to three commands the row held when it was marked.
	Examples []string `json:"examples,omitempty"`
}
