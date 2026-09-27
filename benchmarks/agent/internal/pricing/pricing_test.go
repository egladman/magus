package pricing

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The two models whose cache read is 0.025x base input instead of 0.1x.
var discountedCacheRead = map[string]bool{
	"claude-fable-5-1":  true,
	"claude-mythos-5-1": true,
}

func mustDefault(t *testing.T) Table {
	t.Helper()
	table, err := Default()
	require.NoError(t, err)
	return table
}

func almost(t *testing.T, got, want float64, what string) {
	t.Helper()
	assert.Truef(t, math.Abs(got-want) < 1e-9, "%s = %v, want %v", what, got, want)
}

func TestDefaultTableParses(t *testing.T) {
	t.Parallel()
	table := mustDefault(t)
	url, fetchedOn := table.Source()
	assert.NotEmpty(t, url, "the table records no _source")
	assert.NotEmpty(t, fetchedOn, "the table records no _fetched_on")
	assert.NotEmpty(t, table.Models())
}

// The rule that made the benchmark's cost basis auditable: cache read is a
// tenth of base input on every model, and the two 5.1 models are the documented
// exception at a fortieth. Pinning it here means a transcription slip in
// rates.json fails a test rather than moving a published dollar figure.
func TestCacheReadMultiplier(t *testing.T) {
	t.Parallel()
	table := mustDefault(t)
	for _, model := range table.Models() {
		rates, err := table.Lookup(model)
		require.NoError(t, err)
		want := 0.1
		if discountedCacheRead[model] {
			want = 0.025
		}
		almost(t, rates.CacheRead/rates.Input, want, model+" cache read multiplier")
	}
}

func TestCacheWriteMultipliers(t *testing.T) {
	t.Parallel()
	table := mustDefault(t)
	for _, model := range table.Models() {
		rates, err := table.Lookup(model)
		require.NoError(t, err)
		almost(t, rates.CacheWrite5m/rates.Input, 1.25, model+" 5m cache write multiplier")
		almost(t, rates.CacheWrite1h/rates.Input, 2.0, model+" 1h cache write multiplier")
	}
}

func TestPublishedRates(t *testing.T) {
	t.Parallel()
	table := mustDefault(t)
	for _, tc := range []struct {
		model string
		want  Rates
	}{
		{"claude-fable-5-1", Rates{Input: 10, CacheWrite5m: 12.5, CacheWrite1h: 20, CacheRead: 0.25, Output: 50}},
		{"claude-mythos-5", Rates{Input: 10, CacheWrite5m: 12.5, CacheWrite1h: 20, CacheRead: 1, Output: 50}},
		{"claude-opus-5", Rates{Input: 5, CacheWrite5m: 6.25, CacheWrite1h: 10, CacheRead: 0.5, Output: 25}},
		{"claude-sonnet-5", Rates{Input: 2, CacheWrite5m: 2.5, CacheWrite1h: 4, CacheRead: 0.2, Output: 10}},
		{"claude-sonnet-4-6", Rates{Input: 3, CacheWrite5m: 3.75, CacheWrite1h: 6, CacheRead: 0.3, Output: 15}},
		{"claude-haiku-4-5", Rates{Input: 1, CacheWrite5m: 1.25, CacheWrite1h: 2, CacheRead: 0.1, Output: 5}},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got, err := table.Lookup(tc.model)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestLookupStripsTheDateSuffix(t *testing.T) {
	t.Parallel()
	table := mustDefault(t)
	dated, err := table.Lookup("claude-opus-5-20260301")
	require.NoError(t, err)
	alias, err := table.Lookup("claude-opus-5")
	require.NoError(t, err)
	assert.Equal(t, alias, dated)
}

func TestLookupErrorsOnAnUnknownModel(t *testing.T) {
	t.Parallel()
	table := mustDefault(t)
	for _, model := range []string{"some-unlisted-model", "some-unlisted-model-20260301", ""} {
		_, err := table.Lookup(model)
		require.Error(t, err, "model %q", model)
		assert.Contains(t, err.Error(), "pricing table")
	}
}

// Cost multiplies each counter by its own rate and sums, so a token in one
// category never leaks into another's price.
func TestRatesCost(t *testing.T) {
	t.Parallel()
	table := mustDefault(t)
	rates, err := table.Lookup("claude-sonnet-5")
	require.NoError(t, err)
	got := rates.Cost(1_000_000, 1_000_000, 1_000_000, 1_000_000, 1_000_000)
	almost(t, got, rates.Input+rates.Output+rates.CacheRead+rates.CacheWrite5m+rates.CacheWrite1h, "cost of a million of each")
	almost(t, rates.Cost(0, 0, 0, 0, 0), 0, "cost of nothing")
}

func TestWebSearchIsBilledPerThousand(t *testing.T) {
	t.Parallel()
	table := mustDefault(t)
	almost(t, table.WebSearchUSD(1000), 10, "1000 searches")
	almost(t, table.WebSearchUSD(37), 0.37, "37 searches")
	almost(t, table.WebSearchUSD(0), 0, "no searches")
}

func TestLoadRejectsAMalformedTable(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"no models":     `{"web_search_usd_per_thousand": 10, "models": {}}`,
		"missing rate":  `{"web_search_usd_per_thousand": 10, "models": {"m": {"input": 1, "output": 5, "cache_read": 0.1, "cache_write_5m": 1.25}}}`,
		"zero rate":     `{"web_search_usd_per_thousand": 10, "models": {"m": {"input": 0, "output": 5, "cache_read": 0.1, "cache_write_5m": 1.25, "cache_write_1h": 2}}}`,
		"no surcharges": `{"models": {"m": {"input": 1, "output": 5, "cache_read": 0.1, "cache_write_5m": 1.25, "cache_write_1h": 2}}}`,
		"not json":      `{`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			file := filepath.Join(t.TempDir(), "rates.json")
			require.NoError(t, os.WriteFile(file, []byte(body), 0o644))
			_, err := Load(file)
			assert.Error(t, err)
		})
	}
}

func TestLoadReadsATableFromDisk(t *testing.T) {
	t.Parallel()
	file := filepath.Join(t.TempDir(), "rates.json")
	require.NoError(t, os.WriteFile(file, embedded, 0o644))
	table, err := Load(file)
	require.NoError(t, err)
	assert.Equal(t, mustDefault(t).Models(), table.Models())
}

func TestLoadReportsAMissingFile(t *testing.T) {
	t.Parallel()
	_, err := Load(filepath.Join(t.TempDir(), "absent.json"))
	assert.Error(t, err)
}
