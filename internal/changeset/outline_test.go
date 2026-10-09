package changeset

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestCheckOutlineTrimsAndKeepsOrder(t *testing.T) {
	t.Parallel()

	got, err := CheckOutline([]string{"  is dirty read under mu?  ", "second"})

	require.NoError(t, err)
	assert.Equal(t, []string{"is dirty read under mu?", "second"}, got)
}

// TestCheckOutlineRefusesAnythingThatIsMoreThanAPointer. Each refusal names the reply as the
// person's, because the agent that reads it is the one that tried to write one.
func TestCheckOutlineRefusesAnythingThatIsMoreThanAPointer(t *testing.T) {
	t.Parallel()

	cases := map[string][]string{
		"no topics":       nil,
		"too many":        {"a", "b", "c", "d", "e", "f"},
		"empty":           {"a", "   "},
		"too long":        {strings.Repeat("x", types.DiffOutlineTopicRunes+1)},
		"newline":         {"first\nsecond"},
		"carriage return": {"first\rsecond"},
		"override":        {"safe‮txet"},
	}
	for name, topics := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := CheckOutline(topics)

			require.ErrorIs(t, err, ErrOutline)
			assert.Contains(t, err.Error(), "the person types the reply")
			assert.Nil(t, got)
		})
	}
}

func TestCheckOutlineAcceptsTheBoundsExactly(t *testing.T) {
	t.Parallel()

	topics := make([]string, types.DiffOutlineTopics)
	for i := range topics {
		topics[i] = strings.Repeat("é", types.DiffOutlineTopicRunes)
	}

	got, err := CheckOutline(topics)

	require.NoError(t, err)
	assert.Equal(t, topics, got, "the limit counts characters, not bytes")
}

// TestSetOutlineHoldsOneOutlinePerConversationInMemory. A second outline for the same
// conversation replaces the first, a copy handed out earlier does not change under its reader,
// and nothing about it reaches the drafts file.
func TestSetOutlineHoldsOneOutlinePerConversationInMemory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	s := NewStore(dir)
	s.Attach("/w", "working", types.Diff{}, "x")

	first, err := s.SetOutline("/w", types.DiffOutline{Thread: "c1", Topics: []string{"one"}, AgentName: "a"})
	require.NoError(t, err)
	second, err := s.SetOutline("/w", types.DiffOutline{Thread: "c1", Topics: []string{"two"}})
	require.NoError(t, err)
	other, err := s.SetOutline("/w", types.DiffOutline{Thread: "c2", Topics: []string{"three"}})
	require.NoError(t, err)

	assert.Equal(t, []types.DiffOutline{{Thread: "c1", Topics: []string{"one"}, AgentName: "a"}}, first.Outlines)
	assert.Equal(t, []types.DiffOutline{{Thread: "c1", Topics: []string{"two"}}}, second.Outlines)
	assert.Equal(t, []types.DiffOutline{
		{Thread: "c1", Topics: []string{"two"}},
		{Thread: "c2", Topics: []string{"three"}},
	}, other.Outlines)
	assert.Equal(t, other.Outlines, s.Get("/w").Outlines)
	assert.Empty(t, NewStore(dir).LoadDrafts(), "an outline is never persisted")
}

func TestSetOutlineRefusesWhatCheckOutlineRefuses(t *testing.T) {
	t.Parallel()

	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "x")

	_, err := s.SetOutline("/w", types.DiffOutline{Thread: "c1", Topics: []string{"a\nb"}})
	require.ErrorIs(t, err, ErrOutline)
	_, err = s.SetOutline("/w", types.DiffOutline{Thread: " ", Topics: []string{"a"}})
	require.ErrorIs(t, err, ErrOutline)

	assert.Empty(t, s.Get("/w").Outlines)
}

func TestSetOutlineWithNoSessionReturnsNil(t *testing.T) {
	t.Parallel()

	got, err := NewStore("").SetOutline("/nowhere", types.DiffOutline{Thread: "c1", Topics: []string{"a"}})

	require.NoError(t, err)
	assert.Nil(t, got)
}
