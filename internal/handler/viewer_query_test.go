package handler

import (
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
