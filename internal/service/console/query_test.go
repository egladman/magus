package console

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/journal"
	queryv1 "github.com/egladman/magus/proto/gen/go/magus/query/v1"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestParseEventQuery checks the viewer DSL maps to the right EventQuery fields: known field
// clauses populate typed lists, an unknown key and bare words become (negatable) text matches.
func TestParseEventQuery(t *testing.T) {
	q := ParseEventQuery(`project:web target:build kind:output kind:result status:fail id:foo boom -"cache miss"`)
	assert.Equal(t, []string{"web"}, q.Projects)
	assert.Equal(t, []string{"build"}, q.Targets)
	assert.Equal(t, []string{"output", "result"}, q.Kinds) // repeated field ORs
	assert.Equal(t, "fail", q.Status)
	require.Len(t, q.Text, 3) // id:foo (unknown key), boom, -cache miss
	assert.Equal(t, "id:foo", q.Text[0].Value)
	assert.False(t, q.Text[0].Negate)
	assert.Equal(t, "cache miss", q.Text[2].Value)
	assert.True(t, q.Text[2].Negate)
}

// TestApplyEventQuery checks the filter semantics: fields AND, repeated values OR, text is
// case-insensitive substring, and a negated text match excludes.
func TestApplyEventQuery(t *testing.T) {
	events := []journal.Event{
		{Project: "web", Target: "build", Kind: journal.KindOutput, Text: "compiling module foo"},
		{Project: "web", Target: "test", Kind: journal.KindOutput, Text: "cache miss on run"},
		{Project: "api", Target: "build", Kind: journal.KindResult, Status: journal.StatusFail},
	}

	// Nil query is a no-op.
	assert.Len(t, ApplyEventQuery(events, nil), 3)

	// project:web AND kind:output -> first two.
	got := ApplyEventQuery(events, ParseEventQuery("project:web kind:output"))
	assert.Len(t, got, 2)

	// Case-insensitive substring text match.
	got = ApplyEventQuery(events, ParseEventQuery("COMPILING"))
	require.Len(t, got, 1)
	assert.Equal(t, "compiling module foo", got[0].Text)

	// Negated text excludes the matching event.
	got = ApplyEventQuery(events, ParseEventQuery(`kind:output -"cache miss"`))
	require.Len(t, got, 1)
	assert.Equal(t, "compiling module foo", got[0].Text)
}

// TestParseEventQueryStreamAndLevel covers the two field keys not exercised elsewhere
// (stream, level) so every DSL field maps to its typed list.
func TestParseEventQueryStreamAndLevel(t *testing.T) {
	q := ParseEventQuery("stream:stdout stream:stderr level:error")
	assert.Equal(t, []string{"stdout", "stderr"}, q.Streams)
	assert.Equal(t, []string{"error"}, q.Levels)
}

// TestMatchEventStreamLevelStatus covers the stream/level/status branches of matchEvent that
// the higher-level ApplyEventQuery tests do not reach.
func TestMatchEventStreamLevelStatus(t *testing.T) {
	events := []journal.Event{
		{Stream: journal.StreamStdout, Level: "info", Status: journal.StatusPass, Text: "a"},
		{Stream: journal.StreamStderr, Level: "error", Status: journal.StatusFail, Text: "b"},
	}

	// Stream filter narrows to stderr only.
	got := ApplyEventQuery(events, ParseEventQuery("stream:stderr"))
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].Text)

	// Level filter narrows to info only.
	got = ApplyEventQuery(events, ParseEventQuery("level:info"))
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].Text)

	// Status filter (case-insensitive, single-valued) narrows to fail.
	got = ApplyEventQuery(events, ParseEventQuery("status:FAIL"))
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].Text)
}

// TestMatchEventTimeWindow covers the Since/Until bounds of matchEvent, which the DSL never
// sets (the filter menu sets EventQuery.Time programmatically).
func TestMatchEventTimeWindow(t *testing.T) {
	events := []journal.Event{
		{Ts: 100, Text: "early"},
		{Ts: 200, Text: "mid"},
		{Ts: 300, Text: "late"},
	}

	// Since bound only: drop everything before ts=200.
	q := &viewerv1.EventQuery{Time: &queryv1.TimeRange{Since: timestamppb.New(time.UnixMilli(200))}}
	got := ApplyEventQuery(events, q)
	require.Len(t, got, 2)
	assert.Equal(t, "mid", got[0].Text)

	// Until bound only: drop everything after ts=200.
	q = &viewerv1.EventQuery{Time: &queryv1.TimeRange{Until: timestamppb.New(time.UnixMilli(200))}}
	got = ApplyEventQuery(events, q)
	require.Len(t, got, 2)
	assert.Equal(t, "early", got[0].Text)

	// Both bounds: keep only the single event inside [200,200].
	q = &viewerv1.EventQuery{Time: &queryv1.TimeRange{
		Since: timestamppb.New(time.UnixMilli(200)),
		Until: timestamppb.New(time.UnixMilli(200)),
	}}
	got = ApplyEventQuery(events, q)
	require.Len(t, got, 1)
	assert.Equal(t, "mid", got[0].Text)
}
