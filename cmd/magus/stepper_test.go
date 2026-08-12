package main

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/proc/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStepGateAlwaysBringsItsRepl is the regression guard for a key that was
// advertised and dead.
//
// The gate's prompt offers [r]epl, and the REPL is attached to the context
// separately. Every install site wired the gate and none wired the REPL, so
// pressing r printed "(no REPL available outside a magusfile run)" for as long
// as stepping has existed. Installing them together is what makes that
// impossible; this checks they stay together.
func TestStepGateAlwaysBringsItsRepl(t *testing.T) {
	ctx := withStepGate(context.Background())
	require.NotNil(t, run.StepReplFrom(ctx),
		"the gate offers [r]epl, so the ctx it installs must carry one")
}

func TestStepReplIsAbsentWithoutTheGate(t *testing.T) {
	// And not attached to a context nobody asked to step, so an ordinary run
	// carries nothing extra.
	assert.Nil(t, run.StepReplFrom(context.Background()))
}
