package journal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInvocationFromEvents confirms the run header is reconstructed from the stream's two
// lifecycle events: the started event supplies command/version/start, the finished event
// supplies the end time and outcome: no separate metadata file.
func TestInvocationFromEvents(t *testing.T) {
	events := []Event{
		{Ts: 100, Kind: KindStarted, MagusVersion: "v2", Command: &Command{Arguments: []string{"affected", "ci"}, Trigger: TriggerCI}},
		{Ts: 150, Kind: KindOutput, Text: "building"},
		{Ts: 220, Kind: KindResult, Status: StatusPass},
		{Ts: 230, Kind: KindFinished, Status: StatusFail},
	}
	inv := InvocationFromEvents("inv42", events)
	// Finish is the finished event's timestamp, and the overall outcome comes from it too.
	assert.Equal(t, Invocation{
		ID:           "inv42",
		StartedMs:    100,
		FinishedMs:   230,
		Status:       StatusFail,
		MagusVersion: "v2",
		Command:      Command{Arguments: []string{"affected", "ci"}, Trigger: TriggerCI},
	}, inv)
}

// TestInvocationFromEventsNoFinished confirms an interrupted stream (no finished event)
// falls back to the last event's timestamp for finish.
func TestInvocationFromEventsNoFinished(t *testing.T) {
	inv := InvocationFromEvents("inv1", []Event{
		{Ts: 500, Kind: KindStarted, Command: &Command{Arguments: []string{"run"}}},
		{Ts: 560, Kind: KindOutput, Text: "line"},
	})
	assert.Equal(t, int64(500), inv.StartedMs)
	assert.Equal(t, int64(560), inv.FinishedMs, "no finished event: fall back to last event ts")
	assert.Empty(t, inv.Status)
}
