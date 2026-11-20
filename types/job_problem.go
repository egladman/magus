package types

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/egladman/magus/libs/safetyscissors/job"
)

// JobProblem is one thing wrong with a job record, found before anything is stored or
// run. A validation pass collects every problem rather than stopping at the first, so a
// caller fixes the record in one round.
type JobProblem struct {
	Code DiagnosticCode `json:"code"`
	// Field is a JSON path into the record (`paths.write[2]`, `check.project`,
	// `postconditions[1].symbols[0]`), empty when the problem is the record as a whole.
	Field string `json:"field,omitempty"`
	// Subject is what a [job.Deviation] names to permit this problem: the write path, the
	// `target project` pair, or the output path. Empty on a problem no deviation reaches.
	Subject string `json:"subject,omitempty"`
	// Message says what is wrong, naming the field in its own words.
	Message string `json:"message"`
	// Hint is the edit to the record that clears the problem.
	Hint string `json:"hint"`
	// Postcondition is the postcondition whose addition to the record's postconditions clears the problem, as data.
	// Magus prints it and never applies it: a caller merges `problems[].postcondition` itself.
	Postcondition *Postcondition `json:"postcondition,omitempty"`
	// Deviation is the deviation whose addition to the record's deviations permits the
	// problem, as data, with the same rule as Postcondition. A problem whose fix is one or the other
	// carries only the one that fix names.
	Deviation *job.Deviation `json:"deviation,omitempty"`
}

// Error renders p as a DiagnosticError, so errors.Is matches its code. The text is the
// message, then the fix after a colon.
func (p JobProblem) Error() *DiagnosticError {
	if p.Hint == "" {
		return DiagnosticErrorf(p.Code, "%s", p.Message)
	}
	return DiagnosticErrorf(p.Code, "%s: %s", p.Message, p.Hint)
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
// a declaration's problems report it as JobRecordInvalid.
func (ps JobProblems) Permit(ds []job.Deviation) (kept, permitted JobProblems) {
	for _, p := range ps {
		if p.Permissible() && slices.ContainsFunc(ds, func(d job.Deviation) bool { return DeviationPermits(d, p) }) {
			permitted = append(permitted, p)
			continue
		}
		kept = append(kept, p)
	}
	return kept, permitted
}

// DeviationPermits reports whether d accepts p: the same code, and a subject equal to p's or, for an
// output outside the write paths or an unrun gate, a glob matching it, so `**` accepts
// every regenerated output the integrator owns in one deviation.
func DeviationPermits(d job.Deviation, p JobProblem) bool {
	if d.Code != job.Code(p.Code) {
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

// PermissibleCodes are the codes a deviation may permit, in the order a fix names them:
// each a judgment about how much a job takes on, never a record that is wrong.
func PermissibleCodes() []DiagnosticCode {
	return []DiagnosticCode{WritePathTooBroad, OutputOutsideWritePaths, GateNotRun}
}

// Permissible reports whether a problem of code c may be permitted.
func Permissible(c DiagnosticCode) bool { return slices.Contains(PermissibleCodes(), c) }

// DropsSubject opens the subject of a problem that a re-declaration of a live job would
// erase a declared field: "drops paths.write". A deviation naming it is the record saying so.
const DropsSubject = "drops "

// Permissible reports whether p may be permitted: its code is [Permissible], or it is a
// record refused for dropping a declared field of a live job, which the record may accept
// by naming that field.
func (p JobProblem) Permissible() bool {
	return Permissible(p.Code) || p.Code == JobRecordInvalid && strings.HasPrefix(p.Subject, DropsSubject)
}

// faultCode is the code magus reports a record's own fault under. The module names no
// code, so the kind decides it here.
func faultCode(k job.FaultKind) DiagnosticCode {
	switch k {
	case job.FaultPostcondition:
		return JobPostconditionInvalid
	case job.FaultRecord:
		return JobRecordInvalid
	}
	return JobRecordInvalid
}

// FaultProblems are faults as problems, each under the code its kind maps to, in order.
func FaultProblems(fs []job.Fault) JobProblems {
	if len(fs) == 0 {
		return nil
	}
	ps := make(JobProblems, len(fs))
	for i, f := range fs {
		ps[i] = JobProblem{Code: faultCode(f.Kind), Field: f.Field, Message: f.Message, Hint: f.Fix}
	}
	return ps
}

// LegacySpellings are the members a declaration may still carry under a name the job
// record no longer declares. A decoder reads them beside the record and folds them onto it.
//
// compat(until: no client or stored ledger still sends owned_paths, focus,
// forbidden_paths or a rendered validation line): observe it by grepping the
// leases-*.json archives and the trail for the old keys.
type LegacySpellings struct {
	WritePaths []string `json:"owned_paths,omitempty"`
	DenyPaths  []string `json:"forbidden_paths,omitempty"`
	ReadPaths  []string `json:"focus,omitempty"`
	// Validation is the check as a rendered `magus run` line, which the job decoder parses
	// into Check.
	Validation string `json:"validation,omitempty"`
}

// FoldLegacyNames moves each path field legacy names onto the one d carries it in,
// refusing a field named under both spellings as JobRecordInvalid.
func FoldLegacyNames(d *job.Declaration, legacy LegacySpellings) error {
	var errs []error
	fold := func(name string, into *[]string, from []string) {
		switch {
		case len(from) == 0:
		case len(*into) > 0:
			errs = append(errs, DiagnosticErrorf(JobRecordInvalid, "a declaration names %s or its renamed spelling, not both", name))
		default:
			*into = from
		}
	}
	fold("owned_paths", &d.Paths.Write, legacy.WritePaths)
	fold("forbidden_paths", &d.Paths.Deny, legacy.DenyPaths)
	fold("focus", &d.Paths.Read, legacy.ReadPaths)
	return errors.Join(errs...)
}

// ParseJobTimeout reads a declared timeout as [job.ParseTimeout] does, refusing one that is
// not a positive duration as JobRecordInvalid.
func ParseJobTimeout(s string) (time.Duration, error) {
	d, err := job.ParseTimeout(s)
	if err != nil {
		return 0, DiagnosticErrorf(JobRecordInvalid, "%v", err)
	}
	return d, nil
}

// JobValidation is the result of validating a record against the workspace: the job as it
// would be stored, every problem left after deviations, the ones permitted, and each check that
// could not run.
type JobValidation struct {
	Job       job.Job     `json:"job"`
	Problems  JobProblems `json:"problems"`
	Permitted JobProblems `json:"permitted,omitempty"`
	// Unchecked names each check that could not run and why. A missing workspace or graph
	// is said here, never read as a clean record.
	Unchecked  []string       `json:"unchecked,omitempty"`
	WriteProof job.WriteProof `json:"write_proof,omitempty"`
}

// Error is the validation's problems as one error, nil when none are left.
func (v JobValidation) Error() error { return v.Problems.Error() }
