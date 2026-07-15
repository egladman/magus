package proc

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// NotAdopted classifies a call the daemon did not adopt (non-adoptable subcommand, or
// a build/protocol mismatch) - carried ON the error via NotAdopted() - apart from a
// genuine forward/transport failure. It must see through error wrapping.
func TestNotAdopted(t *testing.T) {
	yes := []error{
		ErrNotAdoptable,
		ErrVersionMismatch,
		ErrProtocolMismatch,
		fmt.Errorf("forwarding run: %w", ErrVersionMismatch),             // wrapped
		fmt.Errorf("%w: run only (only run, affected)", ErrNotAdoptable), // the wrapped-with-context shape
	}
	for _, err := range yes {
		assert.Truef(t, NotAdopted(err), "NotAdopted(%v) should be true", err)
	}

	no := []error{
		errors.New("dial unix: connection refused"), // transport failure
		ErrCycleDetected, // a real error, not a not-adopted refusal
		ErrAlreadyAdopted,
		nil,
	}
	for _, err := range no {
		assert.Falsef(t, NotAdopted(err), "NotAdopted(%v) should be false", err)
	}
}
