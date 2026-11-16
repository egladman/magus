package tier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

var fixtureRows = []Pricing{
	{ID: "claude-opus-5-5", Window: 1_000_000, Input: 4, Output: 20},
	{ID: "claude-haiku-5-5", Window: 1_000_000, Input: 0.1, CacheRead: 0.01, Output: 0.5, LongContext: &PriceStep{Above: 100_000, Multiplier: 5}},
	{ID: "claude-haiku-4-5", Window: 200_000, Input: 1, Output: 5},
}

func fixtureTable() Table { return InEffect(fixtureRows, nil) }

type TableSuite struct{ suite.Suite }

func TestTable(t *testing.T) { suite.Run(t, new(TableSuite)) }

func (s *TableSuite) TestZeroTableHasNoFacts() {
	t := s.T()
	_, ok := Table{}.Lookup("claude-opus-5-5")

	assert.False(t, ok, "no rows is no facts, never a free or zero-sized model")
	assert.Empty(t, Table{}.Rows())
	assert.Empty(t, InEffect(nil, nil).Rows())
}

// A row matches its own id and no other: every spelling a host adds around it is another
// model until a row names it.
func (s *TableSuite) TestLookupMatchesTheExactID() {
	t := s.T()
	table := fixtureTable()
	for _, id := range []string{"claude-opus-5-5", "CLAUDE-OPUS-5-5"} {
		f, ok := table.Lookup(id)
		require.True(t, ok, id)
		assert.Equal(t, fixtureRows[0], f, id)
	}
	for _, id := range []string{
		"", "claude-opus-5-5-20260401", "claude-opus-5-5[1m]", "us.anthropic.claude-opus-5-5-v1:0",
		"anthropic/claude-opus-5-5", "claude-opus-5", "claude-opus-5-55", "gpt-5", "opus",
	} {
		_, ok := table.Lookup(id)
		assert.False(t, ok, id)
	}
}

func (s *TableSuite) TestInEffectOverlaysAndAdds() {
	t := s.T()
	harness := []Pricing{
		{ID: "claude-opus-5-5", Window: 1_000_000, Input: 4, Output: 20},
		{ID: "claude-haiku-5-5", Window: 1_000_000, Input: 0.1, CacheRead: 0.01, Output: 0.5, LongContext: &PriceStep{Above: 100_000, Multiplier: 5}},
	}
	overrides := []Pricing{
		{ID: "Claude-Haiku-5-5", Window: 2_000_000, CacheRead: 0.02},
		{ID: "claude-opus-5-5", LongContext: &PriceStep{Above: 200_000, Multiplier: 2}},
		{ID: "claude-next-1", Window: 3_000_000, LongContext: &PriceStep{Above: 50_000, Multiplier: 2}},
	}

	table := InEffect(harness, overrides)

	assert.Equal(t, []Pricing{
		{ID: "claude-opus-5-5", Window: 1_000_000, Input: 4, Output: 20, LongContext: &PriceStep{Above: 200_000, Multiplier: 2}},
		{ID: "claude-haiku-5-5", Window: 2_000_000, Input: 0.1, CacheRead: 0.02, Output: 0.5, LongContext: &PriceStep{Above: 100_000, Multiplier: 5}},
		{ID: "claude-next-1", Window: 3_000_000, LongContext: &PriceStep{Above: 50_000, Multiplier: 2}},
	}, table.Rows(), "an override's 0 field and nil LongContext keep the harness value")
	assert.Equal(t, int64(1_000_000), harness[1].Window, "InEffect leaves its arguments alone")
	assert.Equal(t, "Claude-Haiku-5-5", overrides[0].ID, "InEffect leaves its arguments alone")
}

// Harness rows go in before agents.models, so a person's row wins field by field.
func (s *TableSuite) TestInEffectLayersHarnessRowsUnderAgentsModels() {
	t := s.T()
	table := InEffect(
		[]Pricing{{ID: "claude-opus-5-5", Window: 1_000_000, Input: 4, Output: 20}},
		[]Pricing{{ID: "claude-opus-5-5", Input: 3}},
	)

	f, ok := table.Lookup("claude-opus-5-5")
	require.True(t, ok)
	assert.Equal(t, Pricing{ID: "claude-opus-5-5", Window: 1_000_000, Input: 3, Output: 20}, f)
}

func (s *TableSuite) TestPriceMultiplierStepsAboveTheThreshold() {
	t := s.T()
	stepped := Pricing{LongContext: &PriceStep{Above: 100_000, Multiplier: 5}}
	assert.InDelta(t, 1.0, stepped.PriceMultiplier(100_000), 0, "the step is above the threshold, not at it")
	assert.InDelta(t, 5.0, stepped.PriceMultiplier(100_001), 0)
	assert.InDelta(t, 1.0, Pricing{}.PriceMultiplier(900_000), 0, "a nil LongContext is no step")
}
