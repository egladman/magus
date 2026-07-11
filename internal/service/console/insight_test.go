package console

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeHistory saves h to a temp file and returns its path.
func writeHistory(t *testing.T, h forecast.History) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, h.Save(context.Background(), path))
	return path
}

// flakeConfigFixture is a config with a low-sample flake threshold so a handful of recorded
// outcomes is enough to produce a non-zero Wilson score.
func flakeConfigFixture(historyPath string) config.Config {
	return config.Config{
		HistoryPath: historyPath,
		Flake: config.Flake{
			Enabled:          true,
			BootstrapSamples: 4,
			MinSamples:       4,
			Threshold:        0.01,
		},
	}
}

func TestServiceFlake(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	outcomes := []forecast.Outcome{
		{Result: "pass", At: now.Add(-3 * time.Hour)},
		{Result: "flake", At: now.Add(-2 * time.Hour)},
		{Result: "pass", At: now.Add(-1 * time.Hour)},
		{Result: "fail", At: now},
	}
	st := forecast.Stats{PassCount: 2, FailCount: 1, FlakeCount: 1, RecentOutcomes: outcomes, LastUpdated: now}
	hist := forecast.History{
		Version: forecast.HistoryVersion,
		Projects: map[string]map[string]forecast.Stats{
			"proj/b": {"test": st},
			"proj/a": {"test": st},
		},
	}
	svc := NewService(nil, flakeConfigFixture(writeHistory(t, hist)), types.StatusBase{}, "1.0.0")

	report, err := svc.Flake(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0.01, report.Threshold)
	require.Len(t, report.Targets, 2)

	// Deterministic (project, target) ordering.
	assert.Equal(t, "proj/a", report.Targets[0].Project)
	assert.Equal(t, "proj/b", report.Targets[1].Project)

	got := report.Targets[0]
	assert.Equal(t, "test", got.Target)
	assert.Equal(t, 2, got.Pass)
	assert.Equal(t, 1, got.Fail)
	assert.Equal(t, 1, got.Flake)
	assert.Equal(t, 4, got.Samples)
	assert.Equal(t, now.Add(-1*time.Hour), got.LastPass, "last pass is the most recent pass/flake outcome")
	assert.Greater(t, got.Score, 0.0, "4 samples at MinSamples=4 produce a non-zero Wilson score")
	assert.True(t, got.Flaky, "score exceeds the 0.01 threshold")
}

func TestServiceFlakeNoHistoryPath(t *testing.T) {
	svc := NewService(nil, config.Config{Flake: config.Flake{Threshold: 0.2}}, types.StatusBase{}, "1.0.0")
	report, err := svc.Flake(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0.2, report.Threshold)
	assert.Empty(t, report.Targets, "no history path yields an empty target list, not an error")
}

func TestServiceInsightNoWorkspace(t *testing.T) {
	svc := NewService(nil, config.Config{}, types.StatusBase{}, "1.0.0")
	_, err := svc.Insight(context.Background())
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

// TestServiceInsightCacheHit checks that within the TTL the underlying scan runs once even
// across several calls.
func TestServiceInsightCacheHit(t *testing.T) {
	var calls atomic.Int64
	view := types.InsightView{Hotspots: types.HotspotOutput{Commits: 7}}
	svc := NewService(nil, config.Config{}, types.StatusBase{}, "1.0.0",
		WithInsightTTL(time.Minute),
		WithInsightFn(func(context.Context) (types.InsightView, error) {
			calls.Add(1)
			return view, nil
		}),
	)

	for i := 0; i < 3; i++ {
		got, err := svc.Insight(context.Background())
		require.NoError(t, err)
		assert.Equal(t, view, got)
	}
	assert.Equal(t, int64(1), calls.Load(), "3 calls within the TTL share one scan")
}

// TestServiceInsightCacheExpires checks the scan reruns once the TTL lapses.
func TestServiceInsightCacheExpires(t *testing.T) {
	var calls atomic.Int64
	svc := NewService(nil, config.Config{}, types.StatusBase{}, "1.0.0",
		WithInsightTTL(time.Millisecond),
		WithInsightFn(func(context.Context) (types.InsightView, error) {
			calls.Add(1)
			return types.InsightView{}, nil
		}),
	)

	_, err := svc.Insight(context.Background())
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	_, err = svc.Insight(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), calls.Load(), "a call past the TTL triggers a fresh scan")
}

// TestServiceInsightCacheDisabled checks that a zero TTL recomputes on every call.
func TestServiceInsightCacheDisabled(t *testing.T) {
	var calls atomic.Int64
	svc := NewService(nil, config.Config{}, types.StatusBase{}, "1.0.0",
		WithInsightTTL(0),
		WithInsightFn(func(context.Context) (types.InsightView, error) {
			calls.Add(1)
			return types.InsightView{}, nil
		}),
	)

	for i := 0; i < 3; i++ {
		_, err := svc.Insight(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, int64(3), calls.Load(), "TTL 0 disables caching")
}
