package job

import (
	"errors"
	"fmt"
)

// CheckSpec is how a host reads the checks its store keeps. The store never interprets a
// [Check]: it validates, compares and renders each one through the host's CheckSpec.
type CheckSpec interface {
	// Validate is c as the store keeps it, or why c is no check. The Check it returns must
	// validate to itself, since the store validates a check again each time it writes the
	// row.
	Validate(c Check) (Check, error)
	// Equal reports whether a and b declare the same check. The store calls it only on
	// checks Validate accepted.
	Equal(a, b Check) (bool, error)
	// Render is c as a message names it, such as the command that runs it.
	Render(c Check) (string, error)
}

// checkSpec is what [WithCheckSpec] gave the store.
type checkSpec struct{ CheckSpec }

// WithCheckSpec is how the store reads the checks a row and its postconditions carry: the store
// keeps a check as an opaque [Check] and never interprets one. Open refuses a nil spec
// with a [*CheckSpecError].
//
// Without it every row write refuses with an [*UnconfiguredError], and [Store.Grade],
// [Store.Witness], [Store.Attest] and [Store.ReportRun] with a
// [*GradingUnconfiguredError], each naming WithCheckSpec.
func WithCheckSpec(spec CheckSpec) StoreOption {
	return func(c *storeConfig) { c.checks = &checkSpec{CheckSpec: spec} }
}

// CheckSpecError is a [WithCheckSpec] given a nil [CheckSpec].
type CheckSpecError struct{}

func (e *CheckSpecError) Error() string {
	return "job: WithCheckSpec needs a CheckSpec, and it was given nil"
}

// refusal is why spec cannot be used, nil when it can or when none was given.
func (spec *checkSpec) refusal() error {
	if spec != nil && spec.CheckSpec == nil {
		return &CheckSpecError{}
	}
	return nil
}

// ErrNoCheck is the zero [Check] where a check is needed: a run of nothing grades nothing,
// and [Check.Decode] has nothing to read.
var ErrNoCheck = errors.New("names no check")

// CheckRefusedError is a check the store's [CheckSpec] refused.
type CheckRefusedError struct {
	// Job is the row the check belongs to, and Field where it sits: "check",
	// "postconditions[<id>].check", "statement.check" or "report.check".
	Job, Field string
	Err        error
}

func (e *CheckRefusedError) Error() string {
	return fmt.Sprintf("job: %s's %s is no check: %v", e.Job, e.Field, e.Err)
}

func (e *CheckRefusedError) Unwrap() error { return e.Err }

// postconditionCheckField is where postcondition id's check sits, as a [CheckRefusedError] names it.
func postconditionCheckField(id string) string {
	if id == PrimaryPostconditionID {
		return "check"
	}
	return fmt.Sprintf("postconditions[%s].check", id)
}

// validateChecks runs the store's [CheckSpec.Validate] over row's check and every postcondition's,
// and keeps the checks it returns.
func (spec *checkSpec) validateChecks(row *Job) error {
	if !row.Check.IsZero() {
		valid, err := spec.Validate(row.Check)
		if err != nil {
			return &CheckRefusedError{Job: row.ID, Field: "check", Err: err}
		}
		row.Check = valid
	}
	for i, g := range row.Postconditions {
		if g.Check.IsZero() {
			continue
		}
		valid, err := spec.Validate(g.Check)
		if err != nil {
			return &CheckRefusedError{Job: row.ID, Field: postconditionCheckField(g.ID), Err: err}
		}
		row.Postconditions[i].Check = valid
	}
	return nil
}

// runCheck is the check a run of job's field records, as the store's
// [CheckSpec.Validate] keeps it.
func (spec *checkSpec) runCheck(job, field string, c Check) (Check, error) {
	if c.IsZero() {
		return Check{}, &CheckRefusedError{Job: job, Field: field, Err: ErrNoCheck}
	}
	valid, err := spec.Validate(c)
	if err != nil {
		return Check{}, &CheckRefusedError{Job: job, Field: field, Err: err}
	}
	return valid, nil
}

// renderCheck is c as a message names it, or a sentence saying it would not render.
func (spec *checkSpec) renderCheck(c Check) string {
	line, err := spec.Render(c)
	if err != nil {
		return fmt.Sprintf("(a check that does not render: %v)", err)
	}
	return line
}

// needChecks refuses grading call op on a store opened without [WithCheckSpec].
func (s *Store) needChecks(op string) error {
	if s.checks == nil {
		return s.wrap(&GradingUnconfiguredError{Op: op, Option: "WithCheckSpec"})
	}
	return nil
}

// host is the [CheckSpec] the store was given, nil when it was given none.
func (spec *checkSpec) host() CheckSpec {
	if spec == nil {
		return nil
	}
	return spec.CheckSpec
}
