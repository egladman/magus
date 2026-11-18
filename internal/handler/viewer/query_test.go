package viewer

import (
	"testing"
	"time"

	"github.com/egladman/magus/libs/safetyscissors/journal"
	queryv1 "github.com/egladman/magus/proto/gen/go/magus/query/v1alpha1"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// at builds a record stamped at ms, for the filter fixtures.
func at[B journal.Body](ms int64, body B) journal.Record {
	return journal.NewRecord(time.UnixMilli(ms), "inv1", body)
}

func text(r journal.Record) string { return flatten(r).text }

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
	events := []journal.Record{
		at(1, journal.Output{Project: "web", Target: "build", Text: "compiling module foo"}),
		at(2, journal.Output{Project: "web", Target: "test", Text: "cache miss on run"}),
		at(3, journal.Run{Project: "api", Target: "build", Status: journal.StatusFail}),
	}

	// Nil query is a no-op.
	assert.Len(t, ApplyEventQuery(events, nil), 3)

	// project:web AND kind:output -> first two.
	got := ApplyEventQuery(events, ParseEventQuery("project:web kind:output"))
	assert.Len(t, got, 2)

	// Case-insensitive substring text match.
	got = ApplyEventQuery(events, ParseEventQuery("COMPILING"))
	require.Len(t, got, 1)
	assert.Equal(t, "compiling module foo", text(got[0]))

	// Negated text excludes the matching event.
	got = ApplyEventQuery(events, ParseEventQuery(`kind:output -"cache miss"`))
	require.Len(t, got, 1)
	assert.Equal(t, "compiling module foo", text(got[0]))
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
	events := []journal.Record{
		at(1, journal.Output{Stream: journal.Stdout, Text: "out-a"}),
		at(2, journal.Output{Stream: journal.Stderr, Text: "out-b"}),
		at(3, journal.Run{Level: "info", Status: journal.StatusPass, Error: "run-a"}),
		at(4, journal.Run{Level: "error", Status: journal.StatusFail, Error: "run-b"}),
	}

	// Stream filter narrows to stderr only.
	got := ApplyEventQuery(events, ParseEventQuery("stream:stderr"))
	require.Len(t, got, 1)
	assert.Equal(t, "out-b", text(got[0]))

	// Level filter narrows to info only.
	got = ApplyEventQuery(events, ParseEventQuery("level:info"))
	require.Len(t, got, 1)
	assert.Equal(t, "run-a", text(got[0]))

	// Status filter (case-insensitive, single-valued) narrows to fail.
	got = ApplyEventQuery(events, ParseEventQuery("status:FAIL"))
	require.Len(t, got, 1)
	assert.Equal(t, "run-b", text(got[0]))
}

// TestMatchEventTimeWindow covers the Since/Until bounds of matchEvent, which the DSL never
// sets (the filter menu sets EventQuery.Time programmatically).
func TestMatchEventTimeWindow(t *testing.T) {
	events := []journal.Record{
		at(100, journal.Output{Text: "early"}),
		at(200, journal.Output{Text: "mid"}),
		at(300, journal.Output{Text: "late"}),
	}

	// Since bound only: drop everything before ts=200.
	q := &viewerv1.EventQuery{Time: &queryv1.TimeRange{Since: timestamppb.New(time.UnixMilli(200))}}
	got := ApplyEventQuery(events, q)
	require.Len(t, got, 2)
	assert.Equal(t, "mid", text(got[0]))

	// Until bound only: drop everything after ts=200.
	q = &viewerv1.EventQuery{Time: &queryv1.TimeRange{Until: timestamppb.New(time.UnixMilli(200))}}
	got = ApplyEventQuery(events, q)
	require.Len(t, got, 2)
	assert.Equal(t, "early", text(got[0]))

	// Both bounds: keep only the single event inside [200,200].
	q = &viewerv1.EventQuery{Time: &queryv1.TimeRange{
		Since: timestamppb.New(time.UnixMilli(200)),
		Until: timestamppb.New(time.UnixMilli(200)),
	}}
	got = ApplyEventQuery(events, q)
	require.Len(t, got, 1)
	assert.Equal(t, "mid", text(got[0]))
}
