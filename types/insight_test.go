package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

// The combined report and the console view both carry the conform lens under one key, so a
// reader of either finds the proposals beside the other lenses.
func TestInsightCarriesConform(t *testing.T) {
	out := ConformOutput{
		Definition: ConformDefinition,
		Rows: []ConformRow{{
			Key:   ConformKey{Metric: ConformFileLines, Population: ConformPopSource},
			Value: 2200, Source: ConformFromAnchor, Propose: 938,
		}},
		Answer: ClassifyAnswer(true, "", nil),
	}
	for name, v := range map[string]any{
		"report": InsightReport{Conform: out},
		"view":   InsightView{Conform: &out},
	} {
		b, err := json.Marshal(v)
		require.NoError(t, err, name)
		var got map[string]any
		require.NoError(t, json.Unmarshal(b, &got), name)
		lens, ok := got["conform"].(map[string]any)
		require.True(t, ok, "%s: conform is %T", name, got["conform"])
		rows, _ := lens["rows"].([]any)
		require.Len(t, rows, 1, name)
		assert.EqualValues(t, 938, rows[0].(map[string]any)["propose"], name)
	}
}

// The lens's contract must not promise that a live percentile is enforced or that magus
// edits the record.
func TestConformDefinitionProposesOnly(t *testing.T) {
	assert.Contains(t, ConformDefinition, "nothing here enforces a live percentile")
	assert.Contains(t, ConformDefinition, "nothing here edits it")
}
