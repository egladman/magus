package console

import (
	"context"
	"time"

	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/types"
)

// historyMetricsCommits caps how many recent commits the git-backed metrics read.
const historyMetricsCommits = 500

// defaultHistoryMetricsTTL is how long a Board's git-backed fields are reused.
const defaultHistoryMetricsTTL = 10 * time.Second

type historyMetricsEntry struct {
	board Board
	at    time.Time
}

// HistoryMetrics assembles the dashboard board: git-backed metrics from one cached
// scan, volatility and model tiers fresh each call, conformance on the cached scan.
func (s *Service) HistoryMetrics(ctx context.Context) (Board, error) {
	board, err := s.cachedHistoryScan(ctx)
	if err != nil {
		return Board{}, err
	}
	report, err := s.volatility(ctx)
	if err != nil {
		return Board{}, err
	}
	board.Volatility = &report
	tiers, err := s.modelTiers(ctx)
	if err != nil {
		return Board{}, err
	}
	board.ModelTiers = &tiers
	return board, nil
}

func (s *Service) modelTiers(ctx context.Context) (types.ModelTiers, error) {
	if s.magus == nil {
		return types.ModelTiers{Definition: types.ModelTiersDefinition, Rows: []types.ModelTierRecord{}}, nil
	}
	return s.magus.ModelTierLens(ctx)
}

func (s *Service) cachedHistoryScan(ctx context.Context) (Board, error) {
	s.historyMetricsMu.Lock()
	defer s.historyMetricsMu.Unlock()

	ttl := s.historyMetricsTTL
	if s.historyMetricsCache != nil && ttl > 0 && time.Since(s.historyMetricsCache.at) < ttl {
		return s.historyMetricsCache.board, nil
	}

	board, err := s.computeHistoryMetrics(ctx)
	if err != nil {
		return Board{}, err
	}
	s.historyMetricsCache = &historyMetricsEntry{board: board, at: time.Now()}
	return board, nil
}

func (s *Service) computeHistoryMetrics(ctx context.Context) (Board, error) {
	if s.historyMetricsFn != nil {
		return s.historyMetricsFn(ctx)
	}
	if s.magus == nil {
		return Board{}, ErrNoWorkspace
	}
	opts := types.HistoryMetricsOptions{Commits: historyMetricsCommits, Files: true}
	hot, err := s.magus.Hotspots(ctx, opts)
	if err != nil {
		return Board{}, err
	}
	aff, err := s.magus.Affinity(ctx, opts)
	if err != nil {
		return Board{}, err
	}
	own, err := s.magus.Ownership(ctx, opts)
	if err != nil {
		return Board{}, err
	}
	tr, err := s.magus.Trend(ctx, opts)
	if err != nil {
		return Board{}, err
	}
	conformance, err := s.magus.Conformance(ctx)
	if err != nil {
		return Board{}, err
	}
	return Board{Hotspots: hot, Affinity: aff, Ownership: own, Trend: tr, Conformance: &conformance}, nil
}

func (s *Service) volatility(ctx context.Context) (types.Volatility, error) {
	return volatility.BuildReport(ctx, s.config.HistoryPath, s.volatilityConfig())
}

func (s *Service) volatilityConfig() volatility.Config {
	return volatility.Config{
		Enabled:          s.config.Volatility.Enabled,
		BootstrapSamples: s.config.Volatility.BootstrapSamples,
		MinSamples:       s.config.Volatility.MinSamples,
		Threshold:        s.config.Volatility.Threshold,
	}
}
