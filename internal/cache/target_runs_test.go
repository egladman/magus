package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Callers racing for one target run it once and all see its error; a failed run is not
// retried within the run.
func TestTargetRunsOnceRunsATargetOnceAndSharesItsOutcome(t *testing.T) {
	runs := NewTargetRuns()
	ref := types.TargetRef{Project: ".", Target: "generate"}
	boom := errors.New("boom")
	var ran atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			err := runs.Once(t.Context(), ref, func() error {
				ran.Add(1)
				time.Sleep(time.Millisecond)
				return boom
			})
			assert.ErrorIs(t, err, boom)
		})
	}
	wg.Wait()
	assert.Equal(t, int32(1), ran.Load())
	assert.ErrorIs(t, runs.Once(t.Context(), ref, func() error { ran.Add(1); return nil }), boom)
	assert.Equal(t, int32(1), ran.Load(), "a failed run is not retried")
}

// A run that panics still releases whoever waits on it, with an error naming the target.
func TestTargetRunsOnceReleasesWaitersWhenTheRunPanics(t *testing.T) {
	runs := NewTargetRuns()
	ref := types.TargetRef{Project: "docs", Target: "generate"}
	started, release := make(chan struct{}), make(chan struct{})
	go func() {
		defer func() { assert.NotNil(t, recover(), "the panic reaches the runner") }()
		_ = runs.Once(context.Background(), ref, func() error {
			close(started)
			<-release
			panic("kaboom")
		})
	}()
	<-started
	waited := make(chan error, 1)
	go func() { waited <- runs.Once(context.Background(), ref, func() error { return nil }) }()
	close(release)
	select {
	case err := <-waited:
		assert.ErrorContains(t, err, "docs:generate panicked")
	case <-time.After(2 * time.Second):
		t.Fatal("a waiter hung after the run panicked")
	}
}

// A cancelled waiter returns at once and leaves the run going.
func TestTargetRunsWaitHonorsItsOwnContext(t *testing.T) {
	runs := NewTargetRuns()
	ref := types.TargetRef{Project: ".", Target: "slow"}
	run, first := runs.TryRun(ref)
	require.True(t, first)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.ErrorIs(t, runs.Once(ctx, ref, func() error { return nil }), context.Canceled)
	run.Complete(nil)
	require.NoError(t, runs.Once(t.Context(), ref, func() error { return errors.New("not run") }))
}

// MarkDone records a pass that already ran a target and never overrides a run already
// reached; Passed lists a project's targets that passed, sorted.
func TestTargetRunsMarkDoneAndPassed(t *testing.T) {
	runs := NewTargetRuns()
	runs.MarkDone(types.TargetRef{Project: ".", Target: "lint"})
	failed := types.TargetRef{Project: ".", Target: "build"}
	require.Error(t, runs.Once(t.Context(), failed, func() error { return errors.New("fail") }))
	runs.MarkDone(failed)
	runs.MarkDone(types.TargetRef{Project: ".", Target: "format"})
	runs.MarkDone(types.TargetRef{Project: "docs", Target: "lint"})

	assert.Equal(t, []string{"format", "lint"}, runs.Passed("."))
	assert.Equal(t, []string{"lint"}, runs.Passed("docs"))
	assert.Error(t, runs.Once(t.Context(), failed, func() error { return nil }), "MarkDone kept the failure")
}
