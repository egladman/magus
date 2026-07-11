package mcp

import (
	"bytes"
	"testing"

	"github.com/egladman/magus/internal/report"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunResultCarriesRefAndFiresChainHint proves the run tools' result text now
// names a target's output ref and that hints.go's chain hint fires on it. It
// reconstructs the exact production path: the run engine records a target.result
// event carrying the execution's ref (report.RunOptions copies cache.Result.Ref),
// the run tool lifts those events into runResult via parseEventLines and marshals
// it as the tool's JSON text, then decorateResult scans that text and appends the
// magus_output chain hint naming the ref.
func TestRunResultCarriesRefAndFiresChainHint(t *testing.T) {
	t.Parallel()

	const ref = "ref1a2b3c4d"

	// Emit a target.result event with a ref, exactly as report.RunOptions does.
	var buf bytes.Buffer
	w := report.NewWriter(&buf, report.WithBlockOnFull())
	require.NoError(t, report.Record(w, report.TargetResult{
		Project: "pkg", Target: "build", Status: "ok", DurationMs: 12, Ref: ref,
	}))
	require.NoError(t, w.Close())

	// Build the tool result the way the run tools do, then marshal it to the JSON
	// text the agent parses.
	out := runResult{OK: true, DurationMs: 12, Events: parseEventLines(&buf)}
	require.Len(t, out.Events, 1, "expected the recorded target.result event")

	result, err := jsonResult(out)
	require.NoError(t, err)

	// firstRef must isolate the ref from the marshalled events.
	assert.Equal(t, ref, firstRef(result), "the run tool result text should carry the output ref")

	// And the chain hint must fire, naming that exact ref.
	decorateResult(result, "magus_run_target")
	assert.Contains(t, resultText(result), "magus_output (ref="+ref+")")
}
