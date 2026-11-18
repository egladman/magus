package job

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/egladman/magus/libs/codec"
)

// CheckValidate reads a check as the caller defines one and returns the bytes the store
// keeps for it, or why raw is no check. The bytes it returns must validate to themselves,
// since the store validates a check again each time it writes the row.
type CheckValidate func(raw codec.RawMessage) (codec.RawMessage, error)

// CheckEqual reports whether a and b declare the same check. The store calls it only on
// checks [CheckValidate] accepted.
type CheckEqual func(a, b codec.RawMessage) (bool, error)

// CheckRender is a check as a message names it, such as the command line that runs it.
type CheckRender func(raw codec.RawMessage) (string, error)

// checkSpec is what [WithCheckSpec] gave the store.
type checkSpec struct {
	validate CheckValidate
	equal    CheckEqual
	render   CheckRender
}

// WithCheckSpec is how the store reads the checks a row and its goals carry: the store
// keeps a check as opaque JSON and never interprets one. Open refuses a nil function with
// a [*CheckSpecError].
//
// Without it every row write refuses with an [*UnconfiguredError], and [Store.Grade],
// [Store.Witness], [Store.Attest] and [Store.ReportRun] with a
// [*GradingUnconfiguredError], each naming WithCheckSpec.
func WithCheckSpec(validate CheckValidate, equal CheckEqual, render CheckRender) StoreOption {
	return func(c *storeConfig) { c.checks = &checkSpec{validate: validate, equal: equal, render: render} }
}

// CheckSpecError is a [WithCheckSpec] given a nil function.
type CheckSpecError struct {
	// Func is the nil one: "validate", "equal" or "render".
	Func string
}

func (e *CheckSpecError) Error() string {
	return fmt.Sprintf("job: WithCheckSpec needs a %s function, and it was given nil", e.Func)
}

// refusal is why spec cannot be used, nil when every function is set.
func (spec *checkSpec) refusal() error {
	switch {
	case spec == nil:
		return nil
	case spec.validate == nil:
		return &CheckSpecError{Func: "validate"}
	case spec.equal == nil:
		return &CheckSpecError{Func: "equal"}
	case spec.render == nil:
		return &CheckSpecError{Func: "render"}
	}
	return nil
}

// ErrNoCheck is a run recorded with no check: a run of nothing grades nothing.
var ErrNoCheck = errors.New("names no check")

// CheckRefusedError is a check the store's [CheckValidate] or [CheckEqual] refused.
type CheckRefusedError struct {
	// Job is the row the check belongs to, and Field where it sits: "check",
	// "goals[<id>].check", "statement.check" or "report.check".
	Job, Field string
	Err        error
}

func (e *CheckRefusedError) Error() string {
	return fmt.Sprintf("job: %s's %s is no check: %v", e.Job, e.Field, e.Err)
}

func (e *CheckRefusedError) Unwrap() error { return e.Err }

// HasCheck reports whether raw declares a check: nil, empty and JSON null declare none.
func HasCheck(raw codec.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}

// goalCheckField is where goal id's check sits, as a [CheckRefusedError] names it.
func goalCheckField(id string) string {
	if id == PrimaryGoalID {
		return "check"
	}
	return fmt.Sprintf("goals[%s].check", id)
}

// validateChecks runs the store's [CheckValidate] over row's check and every goal's, and
// keeps the bytes it returns. A check field that declares none is cleared to nil.
func (spec *checkSpec) validateChecks(row *Job) error {
	if !HasCheck(row.Check) {
		row.Check = nil
	} else {
		valid, err := spec.validate(row.Check)
		if err != nil {
			return &CheckRefusedError{Job: row.ID, Field: "check", Err: err}
		}
		row.Check = valid
	}
	for i, g := range row.Goals {
		if !HasCheck(g.Check) {
			row.Goals[i].Check = nil
			continue
		}
		valid, err := spec.validate(g.Check)
		if err != nil {
			return &CheckRefusedError{Job: row.ID, Field: goalCheckField(g.ID), Err: err}
		}
		row.Goals[i].Check = valid
	}
	return nil
}

// runCheck is the check a run of job's field records, as the store's [CheckValidate]
// keeps it.
func (spec *checkSpec) runCheck(job, field string, raw codec.RawMessage) (codec.RawMessage, error) {
	if !HasCheck(raw) {
		return nil, &CheckRefusedError{Job: job, Field: field, Err: ErrNoCheck}
	}
	valid, err := spec.validate(raw)
	if err != nil {
		return nil, &CheckRefusedError{Job: job, Field: field, Err: err}
	}
	return valid, nil
}

// renderCheck is raw as a message names it, or a sentence saying it would not render.
func (spec *checkSpec) renderCheck(raw codec.RawMessage) string {
	line, err := spec.render(raw)
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
