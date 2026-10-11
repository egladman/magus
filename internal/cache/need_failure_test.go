package cache

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWrapNeedFailureKeepsTheErrorText(t *testing.T) {
	base := errors.New("[MGS4007] .:lint-rules failed")
	wrapped := WrapNeedFailure(fmt.Errorf("ctx.needs: %w", base), "lint-rules", "outaaaaaaaaaaaa")

	assert.Equal(t, "ctx.needs: [MGS4007] .:lint-rules failed", wrapped.Error(), "hopChain and causeSignature read this text")
	require.ErrorIs(t, wrapped, base)
	target, ref := failedNeed(fmt.Errorf("ctx.needs: %w", wrapped))
	assert.Equal(t, "lint-rules", target)
	assert.Equal(t, "outaaaaaaaaaaaa", ref)
}

func TestWrapNeedFailureKeepsTheDeepestMember(t *testing.T) {
	inner := WrapNeedFailure(errors.New("golangci exited 1"), "lint-go", "outbbbbbbbbbbbb")
	outer := WrapNeedFailure(fmt.Errorf("ctx.needs: %w", inner), "lint", "outcccccccccccc")

	target, ref := failedNeed(outer)
	assert.Equal(t, "lint-go", target)
	assert.Equal(t, "outbbbbbbbbbbbb", ref)
}

func TestWrapNeedFailureLeavesWhatItCannotName(t *testing.T) {
	require.NoError(t, WrapNeedFailure(nil, "lint", "outaaaaaaaaaaaa"))

	boom := errors.New("boom")
	assert.Same(t, boom, WrapNeedFailure(boom, "lint", ""), "a member whose output was not persisted has no ref to name")
	target, ref := failedNeed(boom)
	assert.Empty(t, target)
	assert.Empty(t, ref)
}

func TestRunRecordsTheFailedNeedOnTheDescriptor(t *testing.T) {
	step, c := hintCache(t, "package main // failed need")
	boom := WrapNeedFailure(errors.New("lint findings"), "lint-go", "outdddddddddddd")

	res, err := c.Run(context.Background(), step, func(context.Context) error {
		return fmt.Errorf("ctx.needs: %w", boom)
	})
	require.Error(t, err)
	require.NotEmpty(t, res.Ref)

	_, d, err := c.outputs.ByRef(res.Ref)
	require.NoError(t, err)
	assert.Equal(t, OutputDescriptor{
		Failed:        true,
		ErrMsg:        "ctx.needs: lint findings",
		FailedNeed:    "lint-go",
		FailedNeedRef: "outdddddddddddd",
	}, OutputDescriptor{Failed: d.Failed, ErrMsg: d.ErrMsg, FailedNeed: d.FailedNeed, FailedNeedRef: d.FailedNeedRef},
		"the error text is not rewritten; the member rides beside it")
}

func TestRunRecordsNoFailedNeedForAnOrdinaryFailure(t *testing.T) {
	step, c := hintCache(t, "package main // ordinary failure")

	res, err := c.Run(context.Background(), step, func(context.Context) error { return errors.New("boom") })
	require.Error(t, err)

	_, d, err := c.outputs.ByRef(res.Ref)
	require.NoError(t, err)
	assert.Empty(t, d.FailedNeed)
	assert.Empty(t, d.FailedNeedRef)
}

// The unchanged-inputs hint points at the member that holds the findings, not at the
// composed target's own output, which only restates them.
func TestUnchangedFailureHintNamesTheFailedNeedRef(t *testing.T) {
	step, c := hintCache(t, "package main // hint failed need")
	member := WrapNeedFailure(errors.New("lint findings\nmore"), "lint-go", "outeeeeeeeeeeee")
	fn := func(context.Context) error { return member }

	_, err := c.Run(context.Background(), step, fn)
	require.Error(t, err)

	out := captureLog(t, func() {
		_, err = c.Run(context.Background(), step, fn)
		require.Error(t, err)
	})
	assert.Contains(t, out, "inputs unchanged since out")
	assert.Contains(t, out, "magus query output outeeeeeeeeeeee")
	assert.Contains(t, out, "lint-go")
}
