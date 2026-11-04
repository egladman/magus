package console

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

func writeHistory(t *testing.T, h forecast.History) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.json")
	require.NoError(t, forecast.UpdateHistory(t.Context(), path, func(dst *forecast.History) error {
		*dst = h
		return nil
	}))
	return path
}

func volatilityConfigFixture(historyPath string) config.Config {
	return config.Config{
		HistoryPath: historyPath,
		Volatility: config.Volatility{
			Enabled:          true,
			BootstrapSamples: 4,
			MinSamples:       4,
			Threshold:        0.01,
		},
	}
}

func TestServiceVolatility(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	outcomes := []forecast.Outcome{
		{Result: "pass", At: now.Add(-3 * time.Hour)},
		{Result: "volatile", At: now.Add(-2 * time.Hour)},
		{Result: "pass", At: now.Add(-1 * time.Hour)},
		{Result: "fail", At: now},
	}
	st := forecast.Stats{PassCount: 2, FailCount: 1, VolatileCount: 1, RecentOutcomes: outcomes, LastUpdated: now}
	hist := forecast.History{
		Version: forecast.HistoryVersion,
		Projects: map[string]map[string]forecast.Stats{
			"proj/b": {"test": st},
			"proj/a": {"test": st},
		},
	}
	svc := NewService(nil, volatilityConfigFixture(writeHistory(t, hist)), types.StatusBase{}, "1.0.0")

	report, err := svc.volatility(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0.01, report.Threshold)
	require.Len(t, report.Targets, 2)
	assert.Equal(t, "proj/a", report.Targets[0].Project)
	assert.Equal(t, "proj/b", report.Targets[1].Project)
	got := report.Targets[0]
	assert.Greater(t, got.Score, 0.0)
	assert.Equal(t, types.VolatilityTarget{
		Project: "proj/a", Target: "test", Score: got.Score, Volatile: true,
		Pass: 2, Fail: 1, VolatileCount: 1, Samples: 4, LastPass: now.Add(-1 * time.Hour),
	}, got)
}

func TestServiceVolatilityNoHistoryPath(t *testing.T) {
	svc := NewService(nil, config.Config{Volatility: config.Volatility{Threshold: 0.2}}, types.StatusBase{}, "1.0.0")
	report, err := svc.volatility(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0.2, report.Threshold)
	assert.Empty(t, report.Targets)
}

func TestServiceHistoryMetricsNoWorkspace(t *testing.T) {
	svc := NewService(nil, config.Config{}, types.StatusBase{}, "1.0.0")
	_, err := svc.HistoryMetrics(context.Background())
	assert.ErrorIs(t, err, ErrNoWorkspace)
}

func TestServiceHistoryMetricsCacheHit(t *testing.T) {
	var calls atomic.Int64
	board := Board{Hotspots: types.Hotspots{Commits: 7}}
	svc := NewService(nil, config.Config{}, types.StatusBase{}, "1.0.0",
		WithHistoryMetricsTTL(time.Minute),
		WithHistoryMetricsFunction(func(context.Context) (Board, error) {
			calls.Add(1)
			return board, nil
		}),
	)
	want := board
	want.Volatility = &types.Volatility{}
	want.ModelTiers = &types.ModelTiers{Definition: types.ModelTiersDefinition, Rows: []types.ModelTierRecord{}}
	for range 3 {
		got, err := svc.HistoryMetrics(context.Background())
		require.NoError(t, err)
		assert.Equal(t, want, got)
	}
	assert.Equal(t, int64(1), calls.Load())
}

func TestServiceHistoryMetricsCacheExpires(t *testing.T) {
	var calls atomic.Int64
	svc := NewService(nil, config.Config{}, types.StatusBase{}, "1.0.0",
		WithHistoryMetricsTTL(time.Millisecond),
		WithHistoryMetricsFunction(func(context.Context) (Board, error) {
			calls.Add(1)
			return Board{}, nil
		}),
	)
	_, err := svc.HistoryMetrics(context.Background())
	require.NoError(t, err)
	time.Sleep(5 * time.Millisecond)
	_, err = svc.HistoryMetrics(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(2), calls.Load())
}

func TestServiceHistoryMetricsCacheDisabled(t *testing.T) {
	var calls atomic.Int64
	svc := NewService(nil, config.Config{}, types.StatusBase{}, "1.0.0",
		WithHistoryMetricsTTL(0),
		WithHistoryMetricsFunction(func(context.Context) (Board, error) {
			calls.Add(1)
			return Board{}, nil
		}),
	)
	for range 3 {
		_, err := svc.HistoryMetrics(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, int64(3), calls.Load())
}
