package textrank

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shipped returns the embedded weights.json with the first occurrence of old replaced.
func shipped(t *testing.T, old, with string) []byte {
	t.Helper()
	require.Contains(t, string(weightsJSON), old, "the test edits a line that is not in weights.json")
	return bytes.Replace(weightsJSON, []byte(old), []byte(with), 1)
}

func TestLoadWeightsShippedFile(t *testing.T) {
	w, err := LoadWeights()
	require.NoError(t, err)

	assert.Equal(t, Weights{
		Version: 1,
		BM25F: BM25F{
			K1: 1.2,
			Fields: map[string]FieldWeight{
				FieldName:      {Weight: 5, B: 0.75},
				FieldSignature: {Weight: 3, B: 0.75},
				FieldPath:      {Weight: 2, B: 0.75},
				FieldDoc:       {Weight: 1, B: 0.75},
				FieldBody:      {Weight: 1, B: 0.75},
			},
		},
		KindPrior: map[string]float64{},
		PPR: PPRWeights{
			Restart:    0.15,
			Iterations: 20,
			Tolerance:  1e-6,
			MaxNodes:   5000,
			Edges: map[string]float64{
				"references": 1,
				"calls":      1,
				"imports":    0.5,
				"implements": 1,
				"contains":   0.3,
				"co_changes": 0.5,
				"documents":  0.5,
			},
		},
		Fusion:  FusionWeights{K: 60, Lists: map[string]float64{}},
		Context: ContextWeights{Floor: 0, TaskLimit: 8},
	}, w)
}

func TestLoadWeightsDigest(t *testing.T) {
	first, err := LoadWeights()
	require.NoError(t, err)
	second, err := LoadWeights()
	require.NoError(t, err)

	assert.Equal(t, first.Digest(), second.Digest(), "the same file digests the same")
	assert.Len(t, first.Digest(), 64, "hex sha256")

	changed, err := decodeWeights(shipped(t, `"k": 60`, `"k": 61`))
	require.NoError(t, err)
	assert.NotEqual(t, first.Digest(), changed.Digest(), "one changed value changes the digest")
}

func TestLoadWeightsFreeFormMaps(t *testing.T) {
	w, err := decodeWeights(shipped(t, `"kind_prior": {}`, `"kind_prior": {"function": 1.5}`))
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"function": 1.5}, w.KindPrior)

	w, err = decodeWeights(shipped(t, `"lists": {}`, `"lists": {"bm25f": 2}`))
	require.NoError(t, err)
	assert.Equal(t, map[string]float64{"bm25f": 2}, w.Fusion.Lists)
}

func TestLoadWeightsRejects(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		key  string
	}{
		{"unknown top-level key", shipped(t, `"kind_prior": {}`, `"kind_prior": {}, "extra": 1`), "extra"},
		{"unknown nested key", shipped(t, `"restart": 0.15`, `"restart": 0.15, "damping": 1`), "ppr.damping"},
		{"unknown field name", shipped(t, `"body": {"weight": 1`, `"title": {"weight": 1, "b": 0}, "body": {"weight": 1`), "bm25f.fields.title"},
		{"unknown key in a field", shipped(t, `"name": {"weight": 5,`, `"name": {"weight": 5, "boost": 1,`), "bm25f.fields.name.boost"},
		{"missing section", shipped(t, `"fusion": {`, `"fusio": {`), "fusion"},
		{"missing key", shipped(t, `"tolerance": 1e-6,`, ``), "ppr.tolerance"},
		{"missing field", shipped(t, `"doc": {"weight": 1, "b": 0.75},`, ``), "bm25f.fields.doc"},
		{"missing field key", shipped(t, `"name": {"weight": 5, "b": 0.75}`, `"name": {"weight": 5}`), "bm25f.fields.name.b"},
		{"null number", shipped(t, `"k1": 1.2`, `"k1": null`), "bm25f.k1"},
		{"null section", shipped(t, `"kind_prior": {}`, `"kind_prior": null`), "kind_prior"},
		{"negative k1", shipped(t, `"k1": 1.2`, `"k1": -1`), "bm25f.k1"},
		{"negative field weight", shipped(t, `"name": {"weight": 5`, `"name": {"weight": -5`), "bm25f.fields.name.weight"},
		{"negative b", shipped(t, `"name": {"weight": 5, "b": 0.75`, `"name": {"weight": 5, "b": -0.1`), "bm25f.fields.name.b"},
		{"b above 1", shipped(t, `"name": {"weight": 5, "b": 0.75`, `"name": {"weight": 5, "b": 1.5`), "bm25f.fields.name.b"},
		{"negative kind prior", shipped(t, `"kind_prior": {}`, `"kind_prior": {"function": -1}`), "kind_prior.function"},
		{"negative edge", shipped(t, `"calls": 1`, `"calls": -1`), "ppr.edges.calls"},
		{"negative list weight", shipped(t, `"lists": {}`, `"lists": {"bm25f": -1}`), "fusion.lists.bm25f"},
		{"negative fusion k", shipped(t, `"k": 60`, `"k": -60`), "fusion.k"},
		{"negative floor", shipped(t, `"floor": 0`, `"floor": -1`), "context.floor"},
		{"restart zero", shipped(t, `"restart": 0.15`, `"restart": 0`), "ppr.restart"},
		{"restart one", shipped(t, `"restart": 0.15`, `"restart": 1`), "ppr.restart"},
		{"restart above one", shipped(t, `"restart": 0.15`, `"restart": 1.5`), "ppr.restart"},
		{"iterations below one", shipped(t, `"iterations": 20`, `"iterations": 0`), "ppr.iterations"},
		{"zero tolerance", shipped(t, `"tolerance": 1e-6`, `"tolerance": 0`), "ppr.tolerance"},
		{"max nodes below one", shipped(t, `"max_nodes": 5000`, `"max_nodes": 0`), "ppr.max_nodes"},
		{"task limit below one", shipped(t, `"task_limit": 8`, `"task_limit": 0`), "context.task_limit"},
		{"unknown version", shipped(t, `"version": 1`, `"version": 2`), "version"},
		{"not an object", []byte(`[]`), ""},
		{"not json", []byte(`{`), ""},
		{"trailing data", append(bytes.Clone(weightsJSON), []byte(`{}`)...), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeWeights(tc.data)
			require.Error(t, err)
			if tc.key != "" {
				assert.Contains(t, err.Error(), tc.key+":", "the error names the key path")
			}
		})
	}
}
