package main

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/journal"
)

// A failed target's result line has to name the project, the target and the ref that reads
// its log: the reader of a failed gate has only this listing, and a result with no ref sent
// them to a second command to find it.
func TestQueryInvocationTextResultLinesCarryProjectTargetAndRef(t *testing.T) {
	t.Cleanup(snapshotGlobals())
	global = globalFlags{}
	m := newQueryTestWorkspace(t)
	ctx := withMagus(context.Background(), m)

	runCtx, finish := m.BeginInvocation(ctx, journal.Command{Arguments: []string{"run", "lint"}, Trigger: journal.TriggerRun}, "v0.test")
	id := journal.InvocationIDFromContext(runCtx)
	journal.Emit(runCtx, journal.Event{Kind: journal.KindResult, Status: journal.StatusFail, Project: "api", Target: "lint", Ref: "out9bddb214656e", Text: "exit status 1\nsecond line"})
	journal.Emit(runCtx, journal.Event{Kind: journal.KindResult, Status: journal.StatusPass, Project: "web", Target: "build", Ref: "out0123456789ab"})
	finish(errors.New("lint failed"))

	out := captureStdout(t, func() {
		require.NoError(t, queryInvocation(ctx, "", id, false, OutputOptions{Format: FormatText}))
	})

	assert.Contains(t, out, "result    fail api lint out9bddb214656e exit status 1\n", "the first line of the text follows the ref")
	assert.Contains(t, out, "result    pass web build out0123456789ab\n")
	assert.NotContains(t, out, "second line")
}
