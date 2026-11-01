package types

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

func TestConformanceJSONCarriesPropose(t *testing.T) {
	out := Conformance{
		Definition: ConformanceDefinition,
		Rows: []ConformanceRow{{
			Key:   ConformanceKey{Metric: ConformanceFileLines, Population: ConformancePopSource},
			Value: 2200, Propose: 938,
		}},
		Answer: ClassifyAnswer(true, "", nil),
	}
	b, err := json.Marshal(out)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(b, &got))
	rows, _ := got["rows"].([]any)
	require.Len(t, rows, 1)
	assert.EqualValues(t, 938, rows[0].(map[string]any)["propose"])
}

func TestConformanceDefinitionDoesNotPromiseLiveEnforcement(t *testing.T) {
	assert.NotContains(t, ConformanceDefinition, "enforces the live")
	assert.Contains(t, ConformanceDefinition, "proposes")
}

// ADR 0022: nested wire messages are domain nouns; only RPC envelopes use Request/Response.
func TestHistoryMetricsWireUsesDomainNouns(t *testing.T) {
	// go test's cwd is this package dir, regardless of -trimpath.
	repoRoot, err := filepath.Abs("..")
	require.NoError(t, err)
	b, err := os.ReadFile(filepath.Join(repoRoot, "proto/magus/historymetrics/v1alpha1/historymetrics.proto"))
	require.NoError(t, err)
	text := string(b)
	assert.Contains(t, text, "rpc GetHistoryMetrics(GetHistoryMetricsRequest) returns (GetHistoryMetricsResponse)")
	assert.NotContains(t, text, "message Insight")
	assert.NotContains(t, text, "Output {")
	assert.NotContains(t, text, "Report {")
	for _, noun := range []string{"message Hotspots", "message Affinity", "message Ownership", "message Trend", "message Volatility", "message ModelTiers", "message Conformance"} {
		assert.Contains(t, text, noun)
	}
	assert.False(t, strings.Contains(text, "magus.insight"))
}
