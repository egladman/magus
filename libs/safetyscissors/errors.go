package safetyscissors

import (
	"fmt"

	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/tier"
)

// OptionError is [New]'s refusal of an option it was passed, or of a required one it
// was not. New reports every refusal at once, joined.
type OptionError struct {
	// Option is the option's name, such as "WithSlots".
	Option string
	// Problem says what is wrong with it.
	Problem string
}

func (e *OptionError) Error() string {
	return fmt.Sprintf("safetyscissors: option %s: %s", e.Option, e.Problem)
}

// UnconfiguredError is a call that needs a part [New] was not given. It names the
// option that configures the part.
type UnconfiguredError struct {
	// Part is what is missing, such as "slot limiter", and Option the option that
	// supplies it, such as "WithSlots".
	Part, Option string
}

func (e *UnconfiguredError) Error() string {
	return fmt.Sprintf("safetyscissors: the %s is not configured: pass %s to New", e.Part, e.Option)
}

// LeaseNotLiveError is [SafetyScissors.Command] naming a lease whose row is not live. An
// ended lease bounds nothing, so a child started under it would not be confined to it.
type LeaseNotLiveError struct {
	Lease string
	State job.State
}

func (e *LeaseNotLiveError) Error() string {
	return fmt.Sprintf("safetyscissors: lease %q is %s, not live, so it confines nothing", e.Lease, e.State)
}

// AssignmentError is a row whose recorded model tier the configured tier set does not
// declare at the rank it was recorded at. Err is the set's refusal of the name, nil when
// the name parses at another rank.
type AssignmentError struct {
	Job        string
	Assignment tier.Assignment
	Err        error
}

func (e *AssignmentError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("safetyscissors: job %s carries model tier %q, which the tier set does not declare: %v", e.Job, e.Assignment.Name, e.Err)
	}
	return fmt.Sprintf("safetyscissors: job %s carries model tier %q at rank %d, and the tier set ranks it elsewhere", e.Job, e.Assignment.Name, e.Assignment.Rank)
}

func (e *AssignmentError) Unwrap() error { return e.Err }
