package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

// The combined report and the console view both carry the conformance lens under one key, so a
// reader of either finds the proposals beside the other lenses.
func TestInsightCarriesConformance(t *testing.T) {
	out := ConformanceOutput{
		Definition: ConformanceDefinition,
		Rows: []ConformanceRow{{
			Key:   ConformanceKey{Metric: ConformanceFileLines, Population: ConformancePopSource},
			Value: 2200, Source: ConformanceFromAnchor, Propose: 938,
		}},
		Answer: ClassifyAnswer(true, "", nil),
	}
	for name, v := range map[string]any{
		"report": InsightReport{Conformance: out},
		"view":   InsightView{Conformance: &out},
	} {
		b, err := json.Marshal(v)
		require.NoError(t, err, name)
		var got map[string]any
		require.NoError(t, json.Unmarshal(b, &got), name)
		lens, ok := got["conformance"].(map[string]any)
		require.True(t, ok, "%s: conformance is %T", name, got["conformance"])
		rows, _ := lens["rows"].([]any)
		require.Len(t, rows, 1, name)
		assert.EqualValues(t, 938, rows[0].(map[string]any)["propose"], name)
	}
}

// The lens's contract must not promise that a live percentile is enforced or that magus
// edits the record.
func TestConformanceDefinitionProposesOnly(t *testing.T) {
	assert.Contains(t, ConformanceDefinition, "nothing here enforces a live percentile")
	assert.Contains(t, ConformanceDefinition, "nothing here edits it")
}
