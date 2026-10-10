package changeset

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestNormalizeOutlineTrimsAndKeepsOrder(t *testing.T) {
	t.Parallel()

	got, err := NormalizeOutline(types.DiffOutline{
		Thread:    " c1 ",
		Topics:    []string{"  is dirty read under mu?  ", "second"},
		AgentName: "  scout ",
	})

	require.NoError(t, err)
	assert.Equal(t, types.DiffOutline{
		Thread:    "c1",
		Topics:    []string{"is dirty read under mu?", "second"},
		AgentName: "scout",
	}, got)
}

// around puts r between two words, spelled by code point because the characters this test is
// about are the ones nobody can see in the source.
func around(r rune) string { return "left" + string(r) + "right" }

// TestNormalizeOutlineRefusesAnythingThatIsMoreThanAPointer. Each refusal names the reply as the
// person's, because the agent that reads it is the one that tried to write one.
func TestNormalizeOutlineRefusesAnythingThatIsMoreThanAPointer(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", types.DiffOutlineMaxTopicRunes+1)
	one := func(topic string) types.DiffOutline { return types.DiffOutline{Thread: "c1", Topics: []string{topic}} }
	named := func(name string) types.DiffOutline {
		return types.DiffOutline{Thread: "c1", Topics: []string{"a"}, AgentName: name}
	}
	cases := map[string]types.DiffOutline{
		"no thread":            {Thread: " ", Topics: []string{"a"}},
		"no topics":            {Thread: "c1"},
		"too many":             {Thread: "c1", Topics: []string{"a", "b", "c", "d", "e", "f"}},
		"empty":                {Thread: "c1", Topics: []string{"a", "   "}},
		"too long":             one(long),
		"newline":              one("first\nsecond"),
		"carriage return":      one("first\rsecond"),
		"escape":               one(around(0x1b)),
		"bidi override":        one(around(0x202e)),
		"line separator":       one(around(0x2028)),
		"paragraph separator":  one(around(0x2029)),
		"zero width space":     one(around(0x200b)),
		"word joiner":          one(around(0x2060)),
		"soft hyphen":          one(around(0x00ad)),
		"agent name too long":  named(strings.Repeat("n", types.DiffOutlineMaxAgentNameRunes+1)),
		"agent name newline":   named("bot\nsystem"),
		"agent name override":  named(around(0x202e)),
		"agent name separator": named(around(0x2028)),
		"agent name format":    named(around(0x200b)),
	}
	for name, o := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeOutline(o)

			require.ErrorIs(t, err, ErrOutline)
			assert.Contains(t, err.Error(), "not a reply")
			assert.Equal(t, types.DiffOutline{}, got)
		})
	}
}

func TestNormalizeOutlineAcceptsTheBoundsExactly(t *testing.T) {
	t.Parallel()

	topics := make([]string, types.DiffOutlineMaxTopics)
	for i := range topics {
		topics[i] = strings.Repeat("é", types.DiffOutlineMaxTopicRunes)
	}
	in := types.DiffOutline{Thread: "c1", Topics: topics, AgentName: strings.Repeat("é", types.DiffOutlineMaxAgentNameRunes)}

	got, err := NormalizeOutline(in)

	require.NoError(t, err)
	assert.Equal(t, in, got, "the limit counts characters, not bytes")
}

// TestSetOutlineHoldsOneOutlinePerThreadInMemory. A second outline for the same thread replaces
// the first, a copy handed out earlier does not change under its reader, and nothing about it
// reaches the drafts file.
func TestSetOutlineHoldsOneOutlinePerThreadInMemory(t *testing.T) {
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

func TestSetOutlineRefusesWhatNormalizeOutlineRefuses(t *testing.T) {
	t.Parallel()

	s := NewStore("")
	s.Attach("/w", "working", types.Diff{}, "x")

	_, err := s.SetOutline("/w", types.DiffOutline{Thread: "c1", Topics: []string{"a\nb"}})
	require.ErrorIs(t, err, ErrOutline)
	_, err = s.SetOutline("/w", types.DiffOutline{Thread: " ", Topics: []string{"a"}})
	require.ErrorIs(t, err, ErrOutline)

	assert.Empty(t, s.Get("/w").Outlines)
}

// With no session attached there is nowhere to hold the outline, and a caller told it was held
// would report success for an outline nobody will ever see.
func TestSetOutlineWithNoSessionIsAnError(t *testing.T) {
	t.Parallel()

	got, err := NewStore("").SetOutline("/nowhere", types.DiffOutline{Thread: "c1", Topics: []string{"a"}})

	require.ErrorIs(t, err, ErrNoSession)
	assert.Nil(t, got)
}
