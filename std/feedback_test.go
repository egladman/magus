package std

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestFeedbackWindow(t *testing.T) {
	now := time.Date(2026, 9, 30, 22, 0, 0, 0, time.UTC)

	w, err := feedbackWindow(nil, now)
	require.NoError(t, err)
	assert.Equal(t, now.Add(-24*time.Hour), w.Since, "a window nobody names is the last day")
	assert.Equal(t, now, w.Until)

	w, err = feedbackWindow(map[string]any{"session": "s1", "since": "6h", "until": "2026-09-30T20:00:00Z"}, now)
	require.NoError(t, err)
	assert.Equal(t, "s1", w.Session)
	assert.Equal(t, time.Date(2026, 9, 30, 14, 0, 0, 0, time.UTC), w.Since, "a duration counts back from until")

	w, err = feedbackWindow(map[string]any{"since": "2026-09-30T16:00:00Z"}, now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC), w.Since)

	for name, opts := range map[string]map[string]any{
		"unknown option":  {"sinse": "6h"},
		"unreadable time": {"since": "yesterday"},
		"negative window": {"since": "-6h"},
		"until not RFC":   {"until": "now"},
		"empty window":    {"since": "2026-09-30T23:00:00Z"},
		"non-string":      {"session": 3},
	} {
		_, err := feedbackWindow(opts, now)
		assert.Error(t, err, name)
	}
}

func TestDecodeFeedbackMark(t *testing.T) {
	m, err := decodeFeedbackMark(map[string]any{
		"id": "fb0123456789ab", "section": "unguarded", "key": "grep -rn <arg>",
		"verdict": "should-deny", "note": "guard it", "session": "s1",
		"examples": []any{"grep -rn a b"},
	})
	require.NoError(t, err)
	assert.Equal(t, types.FeedbackMark{
		ID: "fb0123456789ab", Section: types.FeedbackUnguarded, Key: "grep -rn <arg>",
		Verdict: types.FeedbackShouldDeny, Note: "guard it", Session: "s1", Examples: []string{"grep -rn a b"},
	}, m)

	_, err = decodeFeedbackMark(map[string]any{"id": 7})
	assert.Error(t, err, "a field of the wrong type raises rather than storing its zero value")
	_, err = decodeFeedbackMark(map[string]any{"examples": []any{1}})
	assert.Error(t, err)
}

func TestFeedbackNeedsAWorkspace(t *testing.T) {
	_, err := FeedbackTrail(context.Background(), nil)
	assert.ErrorContains(t, err, "feedback.trail")
	_, err = FeedbackMarks(context.Background())
	assert.ErrorContains(t, err, "feedback.marks")
}
