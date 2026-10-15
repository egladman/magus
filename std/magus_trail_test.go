package std

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

func TestFeedbackWindow(t *testing.T) {
	now := time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC)

	w, err := feedbackWindow(types.TrailReadOptions{}, now)
	require.NoError(t, err)
	assert.Equal(t, trail.FeedbackWindow{Since: now.Add(-24 * time.Hour), Until: now}, w, "a window nobody names is the last day")

	w, err = feedbackWindow(types.TrailReadOptions{Session: "s1", Since: "6h", Until: "2026-09-30T20:00:00Z"}, now)
	require.NoError(t, err)
	// A duration counts back from until.
	assert.Equal(t, trail.FeedbackWindow{
		Session: "s1",
		Since:   time.Date(2026, 9, 30, 14, 0, 0, 0, time.UTC),
		Until:   time.Date(2026, 9, 30, 20, 0, 0, 0, time.UTC),
	}, w)

	w, err = feedbackWindow(types.TrailReadOptions{Since: "2026-09-30T16:00:00Z"}, now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC), w.Since)

	for name, opts := range map[string]types.TrailReadOptions{
		"unreadable time": {Since: "yesterday"},
		"negative window": {Since: "-6h"},
		"until not RFC":   {Until: "now"},
		"empty window":    {Since: "2026-09-30T23:00:00Z"},
	} {
		_, err := feedbackWindow(opts, now)
		assert.Error(t, err, name)
	}
}

func TestDecodeTrailMark(t *testing.T) {
	m, err := decodeTrailMark(map[string]any{
		"id": "fb0123456789ab", "section": "unguarded", "key": "grep -rn <arg>",
		"verdict": "should-deny", "note": "guard it", "session": "s1",
		"examples": []any{"grep -rn a b"},
	})
	require.NoError(t, err)
	assert.Equal(t, types.TrailMark{
		ID: "fb0123456789ab", Section: types.TrailUnguarded, Key: "grep -rn <arg>",
		Verdict: types.TrailShouldDeny, Note: "guard it", Session: "s1", Examples: []string{"grep -rn a b"},
	}, m)

	_, err = decodeTrailMark(map[string]any{"id": 7})
	assert.Error(t, err, "a field of the wrong type raises rather than storing its zero value")
	_, err = decodeTrailMark(map[string]any{"examples": []any{1}})
	assert.Error(t, err)
}

func TestMagusTrailNeedsAWorkspace(t *testing.T) {
	_, err := MagusTrailRead(context.Background(), types.TrailReadOptions{})
	assert.ErrorContains(t, err, "trail.read")
	_, err = MagusTrailMarks(context.Background())
	assert.ErrorContains(t, err, "trail.marks")
}
