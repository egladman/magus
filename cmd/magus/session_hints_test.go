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

// shellCall builds one loaded shell command. ref keys the dedup, so each one is
// distinct.
func shellCall(session, ref string, ms int64, digest string, served, followed []string) sessions.LoadEvent {
	return sessions.LoadEvent{Session: session, Event: sessions.AgentEvent{
		Host:         "test",
		Kind:         sessions.EventShellCommand,
		Ref:          ref,
		AtMs:         ms,
		Program:      "magus",
		Digest:       digest,
		NextServed:   served,
		NextFollowed: followed,
	}}
}

// One id followed, one rejected for another magus verb, and a reflex beside both: the
// three describe different servings and do not sum to served.
func TestHintUptakeCountsFollowedRejectedAndReflex(t *testing.T) {
	fold := loadedStore(t, []sessions.LoadEvent{
		shellCall("s1", "a1", 1_000, "d-query", []string{"query-explain", "query-path"}, nil),
		shellCall("s1", "a2", 2_000, "d-explain", nil, []string{"query-explain"}),
		shellCall("s1", "a3", 3_000, "d-query", nil, nil),
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

// The table leads with the best-converting hint, and hintUptake is where that order is
// decided; the renderer prints what it is handed.
func TestHintUptakeSortsByRateThenServed(t *testing.T) {
	fold := loadedStore(t, []sessions.LoadEvent{
		shellCall("s1", "a1", 1_000, "", []string{"file-impact"}, nil),
		shellCall("s1", "a2", 2_000, "", []string{"query-explain"}, []string{"file-impact"}),
		shellCall("s1", "a3", 3_000, "", []string{"query-path"}, []string{"query-explain"}),
		shellCall("s1", "a4", 4_000, "", nil, []string{"query-path"}),
		shellCall("s2", "b1", 1_000, "", []string{"query-path"}, nil),
		shellCall("s2", "b2", 2_000, "", nil, nil),
	})

	var ids []string
	for _, r := range hintUptake(fold) {
		ids = append(ids, r.ID)
	}
	assert.Equal(t, []string{"file-impact", "query-explain", "query-path"}, ids,
		"file-impact and query-explain both convert fully, and the better-served one leads")
}

// The floor is named in the output as a note, never applied: which hints go is a
// decision, and magus informs.
func TestRenderHintUptakeNamesTheFloor(t *testing.T) {
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
	assert.True(t, strings.Index(out, "query-explain") < strings.Index(out, "file-impact"),
		"the renderer preserves the order it is handed")
	assert.Contains(t, out, "40.0%")
	assert.Contains(t, out, "5.0%")
	assert.Contains(t, out, "15%", "the floor is reported as a note")
}

// A transcript loaded before its journal lines existed carries no ids, and a re-load
// is what repairs it. Without that the stamp is one-shot: the dedup key does not name
// the ids, so every line written after the first load would be uncountable forever.
func TestHintUptakeRecoversIDsOnASecondLoad(t *testing.T) {
	dir := t.TempDir()
	early := []sessions.LoadEvent{
		shellCall("s1", "a1", 1_000, "", nil, nil),
		shellCall("s1", "a2", 2_000, "", nil, nil),
	}
	_, err := sessions.LoadEvents(dir, early, sessions.SessionStart{Workspace: dir})
	require.NoError(t, err)

	joined := []sessions.LoadEvent{
		shellCall("s1", "a1", 1_000, "", []string{"query-explain"}, nil),
		shellCall("s1", "a2", 2_000, "", nil, []string{"query-explain"}),
	}
	_, err = sessions.LoadEvents(dir, joined, sessions.SessionStart{Workspace: dir})
	require.NoError(t, err)

	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	assert.Equal(t, []hintUptakeRow{{ID: "query-explain", Served: 1, Followed: 1, Rate: 1}}, hintUptake(fold),
		"one call, counted once, with the ids the second load recovered")
}

// An empty store says so and names the command that fills it: nothing served is a
// fact about the store, not a verdict on any hint.
func TestRenderHintUptakeOnAnEmptyStore(t *testing.T) {
	var buf bytes.Buffer
	renderHintUptake(&buf, hintUptakeReport{Floor: hintFloor, Lookahead: nextLookahead})
	assert.Contains(t, buf.String(), "magus session load")
}
