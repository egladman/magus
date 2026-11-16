package main

import (
	"context"
	"fmt"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/libs/clikit/exit"
)

// TestWithInterruptPrefersASpecificCode keeps 128+N from flattening a code the
// command already chose.
func TestWithInterruptPrefersASpecificCode(t *testing.T) {
	t.Parallel()

	none := func() (syscall.Signal, bool) { return 0, false }
	term := func() (syscall.Signal, bool) { return syscall.SIGTERM, true }
	intr := func() (syscall.Signal, bool) { return syscall.SIGINT, true }

	assert.Equal(t, 0, withInterrupt(0, nil, none), "an uninterrupted success stays 0")
	assert.Equal(t, 1, withInterrupt(1, nil, none), "an ordinary failure is untouched")
	assert.Equal(t, 130, withInterrupt(0, nil, intr), "SIGINT reports 128+2")
	assert.Equal(t, 143, withInterrupt(0, nil, term), "SIGTERM reports 128+15")
	assert.Equal(t, exit.Usage, withInterrupt(exit.Usage, nil, term),
		"a usage error keeps its own code rather than being flattened to 143")

	// A command that RETURNS the cancellation instead of swallowing it (awaitInvocation
	// returns ctx.Err()) reached mapExitCode as a generic failure and reported 1, which
	// says the work failed about a run the user stopped.
	cancelled := fmt.Errorf("--wait: %w", context.Canceled)
	assert.Equal(t, 130, withInterrupt(1, cancelled, intr), "a surfaced cancellation is the signal")
	assert.Equal(t, 1, withInterrupt(1, cancelled, none),
		"without a signal it is an ordinary cancellation and keeps its code")
}
