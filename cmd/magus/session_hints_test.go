package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/sessions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loadedStore writes events into a temp session store and folds it back, so the lens
// is exercised against the store it actually reads rather than a hand-built Fold.
func loadedStore(t *testing.T, events []sessions.LoadEvent) sessions.Fold {
	t.Helper()
	dir := t.TempDir()
	_, err := sessions.LoadEvents(dir, events, sessions.SessionStart{Workspace: dir})
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	return fold
}

// call builds one loaded shell command. ref keys the dedup, so each one is distinct.
func call(session, ref string, ms int64, digest string, served, followed []string) sessions.LoadEvent {
	return sessions.LoadEvent{Session: session, Event: sessions.AgentEvent{
		Host:         "test",
		Kind:         sessions.EventShellCommand,
		Ref:          ref,
		AtMs:         ms,
		Program:      "magus",
		Digest:       digest,
		NextIDs:      served,
		NextFollowed: followed,
	}}
}

// One id followed, one rejected in favour of another magus verb, and a reflex beside
// both: the three describe different servings and do not sum to served.
func TestHintUptakeCountsFollowedRejectedAndReflex(t *testing.T) {
	fold := loadedStore(t, []sessions.LoadEvent{
		call("s1", "a1", 1_000, "d-query", []string{"query-explain", "query-path"}, nil),
		call("s1", "a2", 2_000, "d-explain", nil, []string{"query-explain"}),
		call("s1", "a3", 3_000, "d-query", nil, nil),
	})

	rows := map[string]hintUptakeRow{}
	for _, r := range hintUptake(fold) {
		rows[r.ID] = r
	}

	explain := rows["query-explain"]
	assert.Equal(t, 1, explain.Served)
	assert.Equal(t, 1, explain.Followed)
	assert.Equal(t, 0, explain.Rejected)
	assert.InDelta(t, 1.0, explain.Rate, 0.001)

	path := rows["query-path"]
	assert.Equal(t, 1, path.Served)
	assert.Equal(t, 0, path.Followed)
	assert.Equal(t, 1, path.Rejected, "a magus verb that was not the hinted one is a rejection")
	assert.Equal(t, 1, path.Reflex, "the same command ran again inside the window")
	assert.Zero(t, path.Rate)
}

// The table leads with the best-converting hint and names the floor, so a reader sees
// what to prune without being told to prune it.
func TestRenderHintUptakeSortsByRateAndNamesTheFloor(t *testing.T) {
	var buf bytes.Buffer
	renderHintUptake(&buf, hintUptakeReport{
		Floor:     hintFloor,
		Lookahead: nextLookahead,
		Hints: []hintUptakeRow{
			{ID: "query-explain", Served: 10, Followed: 4, Rejected: 5, Reflex: 1, Rate: 0.4},
			{ID: "file-impact", Served: 20, Followed: 1, Rejected: 12, Rate: 0.05},
		},
	})

	out := buf.String()
	assert.True(t, strings.Index(out, "query-explain") < strings.Index(out, "file-impact"))
	assert.Contains(t, out, "40.0%")
	assert.Contains(t, out, "5.0%")
	assert.Contains(t, out, "15%", "the floor is reported as a note")
}

// An empty store says so and names the command that fills it: nothing served is a
// fact about the store, not a verdict on any hint.
func TestRenderHintUptakeOnAnEmptyStore(t *testing.T) {
	var buf bytes.Buffer
	renderHintUptake(&buf, hintUptakeReport{Floor: hintFloor, Lookahead: nextLookahead})
	assert.Contains(t, buf.String(), "magus session load")
}
