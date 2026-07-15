package journal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestInvocationFromEvents confirms the run header is reconstructed from the stream's two
// lifecycle events: the started event supplies command/version/start, the finished event
// supplies the end time and outcome - no separate metadata file.
func TestInvocationFromEvents(t *testing.T) {
	events := []Event{
		{Ts: 100, Kind: KindStarted, MagusVersion: "v2", Command: &Command{Arguments: []string{"affected", "ci"}, Trigger: TriggerCI}},
		{Ts: 150, Kind: KindOutput, Text: "building"},
		{Ts: 220, Kind: KindResult, Status: StatusPass},
		{Ts: 230, Kind: KindFinished, Status: StatusFail},
	}
	inv := InvocationFromEvents("inv42", events)
	assert.Equal(t, "inv42", inv.ID)
	assert.Equal(t, int64(100), inv.StartedMs)
	assert.Equal(t, int64(230), inv.FinishedMs, "finish is the finished event's timestamp")
	assert.Equal(t, StatusFail, inv.Status, "overall outcome comes from the finished event")
	assert.Equal(t, "v2", inv.MagusVersion)
	assert.Equal(t, Command{Arguments: []string{"affected", "ci"}, Trigger: TriggerCI}, inv.Command)
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
