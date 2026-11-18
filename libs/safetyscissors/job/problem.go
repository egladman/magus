package job

import (
	"fmt"
)

// Code names a class of problem in the caller's own vocabulary, such as its
// diagnostic codes. This module defines none: it carries a code a caller gave it and
// compares it with the ones the caller permits.
type Code string

// Deviation permits one permissible problem on a record's own authority, with the reason a
// reviewer reads: "the root regenerates it", "the root runs it". The name is MISRA C's: a
// deviation record sanctions a violation with its rationale.
type Deviation struct {
	Code    Code   `json:"code"`
	Subject string `json:"subject"`
	Reason  string `json:"reason" schema:"nonblank"`
}

// FaultKind is which rule a fault breaks. The caller maps each to its own code.
type FaultKind string

const (
	// FaultRecord is a record field that fails its own shape.
	FaultRecord FaultKind = "record"
	// FaultPostcondition is a postcondition no evidence could ever grade.
	FaultPostcondition FaultKind = "postcondition"
)

// Fault is one thing wrong with a record on its own, found without a workspace. It names
// no diagnostic code, since the codes are the caller's: the caller maps Kind to one.
type Fault struct {
	Kind FaultKind
	// Field is a JSON path into the record (`paths.write[2]`, `postconditions[1].symbols[0]`),
	// empty when the fault is the record as a whole.
	Field string
	// Message says what is wrong, naming the field in its own words.
	Message string
	// Fix is the edit to the record that clears the fault, empty when none is named.
	Fix string
}

// Error is the message, then the fix after a colon.
func (f Fault) Error() string {
	if f.Fix == "" {
		return f.Message
	}
	return fmt.Sprintf("%s: %s", f.Message, f.Fix)
}
