package types

import (
	"errors"
	"slices"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// JobProblem is one thing wrong with a job record, found before anything is stored or
// run. A validation pass collects every problem rather than stopping at the first, so a
// caller fixes the record in one round.
type JobProblem struct {
	Code DiagnosticCode `json:"code"`
	// Field is a JSON path into the record (`paths.write[2]`, `check.project`,
	// `goals[1].symbols[0]`), empty when the problem is the record as a whole.
	Field string `json:"field,omitempty"`
	// Subject is what a [Deviation] names to permit this problem: the write path, the
	// `target project` pair, or the output path. Empty on a problem no deviation reaches.
	Subject string `json:"subject,omitempty"`
	// Message says what is wrong, naming the field in its own words.
	Message string `json:"message"`
	// Fix is the edit to the record that clears the problem.
	Fix string `json:"fix"`
}

// Error renders p as a DiagnosticError, so errors.Is matches its code. The text is the
// message, then the fix after a colon.
func (p JobProblem) Error() *DiagnosticError {
	if p.Fix == "" {
		return DiagnosticErrorf(p.Code, "%s", p.Message)
	}
	return DiagnosticErrorf(p.Code, "%s: %s", p.Message, p.Fix)
}

// JobProblems are the problems of one record, in record order.
type JobProblems []JobProblem

// Error joins each problem as a [*DiagnosticError] in record order, or returns nil when there
// are none.
func (ps JobProblems) Error() error {
	if len(ps) == 0 {
		return nil
	}
	errs := make([]error, len(ps))
	for i, p := range ps {
		errs[i] = p.Error()
	}
	return errors.Join(errs...)
}

// Permit splits ps into the problems a deviation in ds matches by code and subject and the
// ones it does not. A deviation on a code that is not [Permissible] matches nothing;
// [Declaration.Problems] reports it as JobRecordInvalid.
func (ps JobProblems) Permit(ds []Deviation) (kept, permitted JobProblems) {
	for _, p := range ps {
		if p.Permissible() && slices.ContainsFunc(ds, func(d Deviation) bool { return d.Permits(p) }) {
			permitted = append(permitted, p)
			continue
		}
		kept = append(kept, p)
	}
	return kept, permitted
}

// Deviation permits one permissible problem on a record's own authority, with the reason a
// reviewer reads: "the root regenerates it", "the root runs it". The name is MISRA C's: a
// deviation record sanctions a violation with its rationale.
type Deviation struct {
	Code    DiagnosticCode `json:"code"`
	Subject string         `json:"subject"`
	Reason  string         `json:"reason" schema:"nonblank"`
}

// Permits reports whether d accepts p: the same code, and a subject equal to p's or, for an
// output outside the write paths or an unrun gate, a glob matching it, so `**` accepts
// every regenerated output the integrator owns in one deviation.
func (d Deviation) Permits(p JobProblem) bool {
	if d.Code != p.Code {
		return false
	}
	if d.Subject == p.Subject {
		return true
	}
	if p.Code != OutputOutsideWritePaths && p.Code != GateNotRun {
		return false
	}
	matched, _ := doublestar.Match(d.Subject, p.Subject)
	return matched
}

// DropsSubject opens the subject of a problem that a re-declaration of a live job would
// erase a declared field: "drops paths.write". A deviation naming it is the record saying so.
const DropsSubject = "drops "

// Permissible reports whether p may be permitted: its code is [Permissible], or it is a
// record refused for dropping a declared field of a live job, which the record may accept
// by naming that field.
func (p JobProblem) Permissible() bool {
	return Permissible(p.Code) || p.Code == JobRecordInvalid && strings.HasPrefix(p.Subject, DropsSubject)
}

// Permissible reports whether a problem of code c may be permitted: only a judgment about how
// much a job takes on, never a record that is wrong.
func Permissible(c DiagnosticCode) bool {
	switch c {
	case WritePathTooBroad, OutputOutsideWritePaths, GateNotRun:
		return true
	}
	return false
}

// JobValidation is the result of validating a record against the workspace: the job as it
// would be stored, every problem left after deviations, the ones permitted, and each check that
// could not run.
type JobValidation struct {
	Job       Job         `json:"job"`
	Problems  JobProblems `json:"problems"`
	Permitted JobProblems `json:"permitted,omitempty"`
	// Unchecked names each check that could not run and why. A missing workspace or graph
	// is said here, never read as a clean record.
	Unchecked  []string      `json:"unchecked,omitempty"`
	WriteProof JobWriteProof `json:"write_proof,omitempty"`
}

// Error is the validation's problems as one error, nil when none are left.
func (v JobValidation) Error() error { return v.Problems.Error() }
