package types

import (
	"errors"
	"fmt"
	"slices"

	"github.com/bmatcuk/doublestar/v4"
)

// JobProblem is one thing wrong with a job record, found before anything is stored or
// run. A validation pass collects every problem rather than stopping at the first, so a
// caller fixes the record in one round.
type JobProblem struct {
	Code DiagnosticCode `json:"code"`
	// Field is a JSON path into the record (`write_paths[2]`, `check.project`,
	// `goals[1].symbols[0]`), empty when the problem is the record as a whole.
	Field string `json:"field,omitempty"`
	// Subject is what a [JobWaiver] names to waive this problem: the write path, the
	// `target project` pair, or the output path. Empty on a problem no waiver reaches.
	Subject string `json:"subject,omitempty"`
	// Message says what is wrong, naming the field in its own words.
	Message string `json:"message"`
	// Fix is the edit to the record that clears the problem.
	Fix string `json:"fix"`
	// Edit is Fix as data, set only where the fix needs no judgment: `--fix` applies it. A
	// problem without one is the caller's to decide.
	Edit *JobEdit `json:"edit,omitempty"`
}

// JobEdit is one mechanical change to a record. Exactly one field is set.
type JobEdit struct {
	AddWritePath string     `json:"add_write_path,omitempty"`
	AddGoal      *Goal      `json:"add_goal,omitempty"`
	AddWaiver    *JobWaiver `json:"add_waiver,omitempty"`
}

// Apply makes e on d and reports whether d changed: an entry d already holds, or a goal id
// it already uses, is left alone.
func (e JobEdit) Apply(d *Declaration) bool {
	switch {
	case e.AddWritePath != "":
		if slices.Contains(d.WritePaths, e.AddWritePath) {
			return false
		}
		d.WritePaths = append(d.WritePaths, e.AddWritePath)
	case e.AddGoal != nil:
		if slices.ContainsFunc(d.Goals, func(g Goal) bool { return g.ID == e.AddGoal.ID }) {
			return false
		}
		d.Goals = append(d.Goals, *e.AddGoal)
	case e.AddWaiver != nil:
		if slices.Contains(d.Waivers, *e.AddWaiver) {
			return false
		}
		d.Waivers = append(d.Waivers, *e.AddWaiver)
	default:
		return false
	}
	return true
}

// String is e as a person reads it in a list of what `--fix` did.
func (e JobEdit) String() string {
	switch {
	case e.AddWritePath != "":
		return fmt.Sprintf("added %q to write_paths", e.AddWritePath)
	case e.AddGoal != nil:
		return fmt.Sprintf("added the goal %q (%s)", e.AddGoal.ID, e.AddGoal.Check)
	case e.AddWaiver != nil:
		return fmt.Sprintf("waived %s %q (%s)", e.AddWaiver.Code, e.AddWaiver.Subject, e.AddWaiver.Reason)
	}
	return "no edit"
}

// Err renders p as a DiagnosticError, so errors.Is matches its code. The text is the
// message, then the fix after a colon.
func (p JobProblem) Err() *DiagnosticError {
	if p.Fix == "" {
		return DiagnosticErrorf(p.Code, "%s", p.Message)
	}
	return DiagnosticErrorf(p.Code, "%s: %s", p.Message, p.Fix)
}

// JobProblems are the problems of one record, in record order.
type JobProblems []JobProblem

// Err joins each problem as a [*JobProblemError] in record order, or returns nil when there
// are none.
func (ps JobProblems) Err() error {
	if len(ps) == 0 {
		return nil
	}
	errs := make([]error, len(ps))
	for i, p := range ps {
		errs[i] = &JobProblemError{Problem: p}
	}
	return errors.Join(errs...)
}

// JobProblemError is a problem as an error that keeps the problem, so a caller handed the
// joined error recovers each one's Edit. It reads and unwraps as [JobProblem.Err].
type JobProblemError struct {
	Problem JobProblem
}

func (e *JobProblemError) Error() string { return e.Problem.Err().Error() }
func (e *JobProblemError) Unwrap() error { return e.Problem.Err() }

// Fixable counts the problems of ps that carry an Edit.
func (ps JobProblems) Fixable() int {
	n := 0
	for _, p := range ps {
		if p.Edit != nil {
			n++
		}
	}
	return n
}

// Waive splits ps into the problems a waiver in ws matches by code and subject and the
// ones it does not. A waiver on a code that is not [Waivable] matches nothing;
// [Declaration.Problems] reports it as JobRecordInvalid.
func (ps JobProblems) Waive(ws []JobWaiver) (kept, waived JobProblems) {
	for _, p := range ps {
		if Waivable(p.Code) && slices.ContainsFunc(ws, func(w JobWaiver) bool { return w.Waives(p) }) {
			waived = append(waived, p)
			continue
		}
		kept = append(kept, p)
	}
	return kept, waived
}

// JobWaiver accepts one waivable problem on a record's own authority, with the reason a
// reviewer reads: "the root regenerates it", "the root runs it".
type JobWaiver struct {
	Code    DiagnosticCode `json:"code"`
	Subject string         `json:"subject"`
	Reason  string         `json:"reason" schema:"nonblank"`
}

// Waives reports whether w accepts p: the same code, and a subject equal to p's or, for an
// output outside the write paths or an unrun gate, a glob matching it, so `**` accepts
// every regenerated output the integrator owns in one waiver.
func (w JobWaiver) Waives(p JobProblem) bool {
	if w.Code != p.Code {
		return false
	}
	if w.Subject == p.Subject {
		return true
	}
	if p.Code != OutputOutsideWritePaths && p.Code != GateNotRun {
		return false
	}
	matched, _ := doublestar.Match(w.Subject, p.Subject)
	return matched
}

// Waivable reports whether a problem of code c may be waived: only a judgment about how
// much a job takes on, never a record that is wrong.
func Waivable(c DiagnosticCode) bool {
	switch c {
	case WritePathTooBroad, OutputOutsideWritePaths, GateNotRun:
		return true
	}
	return false
}

// JobValidation is the result of validating a record against the workspace: the job as it
// would be stored, every problem left after waivers, the ones waived, and each check that
// could not run.
type JobValidation struct {
	Job      Job         `json:"job"`
	Problems JobProblems `json:"problems"`
	Waived   JobProblems `json:"waived,omitempty"`
	// Unchecked names each check that could not run and why. A missing workspace or graph
	// is said here, never read as a clean record.
	Unchecked  []string      `json:"unchecked,omitempty"`
	WriteProof JobWriteProof `json:"write_proof,omitempty"`
}

// Err is the validation's problems as one error, nil when none are left.
func (v JobValidation) Err() error { return v.Problems.Err() }
