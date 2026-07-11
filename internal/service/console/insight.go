package console

import (
	"context"
	"errors"
	"time"

	"github.com/egladman/magus/internal/ci/volatility"
	"github.com/egladman/magus/types"
)

// ErrNoWorkspace is returned by Insight when the service was built without a *magus.Magus
// (the graph/scan seams are unset too). The daemon always supplies one, so this only fires
// on a misconfigured or test service; handlers map it to a 503 rather than a 500.
var ErrNoWorkspace = errors.New("console: no workspace available")

// insightCommits caps how many recent commits the insight scan reads, matching the
// `magus insight` CLI default so the console and CLI agree on the window.
const insightCommits = 500

// defaultInsightTTL is how long an assembled InsightView is reused before the git-log scan
// is run again. It exists so repeated dashboard polls collapse onto one scan instead of
// re-shelling git per request; WithInsightTTL overrides it (zero disables caching).
const defaultInsightTTL = 10 * time.Second

// insightEntry is one cached InsightView plus the wall-clock time it was assembled.
type insightEntry struct {
	view types.InsightView
	at   time.Time
}

// Insight assembles the five insight lenses. The four VCS-history lenses (hotspots, affinity,
// ownership, trend) come from one bounded git-log scan cached for insightTTL, so a burst of
// dashboard polls does not re-shell git on every request. The fifth lens, volatility, rides
// fresh on every call: it is a cheap runtime-history file read that changes on every run, so a
// TTL-stale copy would mislead the dashboard. The result is one InsightView carrying every lens.
func (s *Service) Insight(ctx context.Context) (types.InsightView, error) {
	view, err := s.cachedScan(ctx)
	if err != nil {
		return types.InsightView{}, err
	}
	report, err := s.volatility(ctx)
	if err != nil {
		return types.InsightView{}, err
	}
	view.Volatility = &report
	return view, nil
}

// cachedScan returns the four VCS-history lenses, reusing a scan within the TTL. Assembly is
// serialized by the cache mutex, so concurrent pollers past a cold TTL wait for one scan rather
// than each launching their own. Volatility is not part of this cache - Insight folds it in fresh.
func (s *Service) cachedScan(ctx context.Context) (types.InsightView, error) {
	s.insightMu.Lock()
	defer s.insightMu.Unlock()

	ttl := s.insightTTL
	if s.insightCache != nil && ttl > 0 && time.Since(s.insightCache.at) < ttl {
		return s.insightCache.view, nil
	}

	view, err := s.computeInsight(ctx)
	if err != nil {
		return types.InsightView{}, err
	}
	s.insightCache = &insightEntry{view: view, at: time.Now()}
	return view, nil
}

// computeInsight runs the lenses against the workspace (or the injected seam in tests).
func (s *Service) computeInsight(ctx context.Context) (types.InsightView, error) {
	if s.insightFn != nil {
		return s.insightFn(ctx)
	}
	if s.magus == nil {
		return types.InsightView{}, ErrNoWorkspace
	}
	opts := types.InsightOptions{Commits: insightCommits}
	hot, err := s.magus.Hotspots(ctx, opts)
	if err != nil {
		return types.InsightView{}, err
	}
	aff, err := s.magus.Affinity(ctx, opts)
	if err != nil {
		return types.InsightView{}, err
	}
	own, err := s.magus.Ownership(ctx, opts)
	if err != nil {
		return types.InsightView{}, err
	}
	tr, err := s.magus.Trend(ctx, opts)
	if err != nil {
		return types.InsightView{}, err
	}
	return types.InsightView{Hotspots: hot, Affinity: aff, Ownership: own, Trend: tr}, nil
}

// volatility reports per-(project, target) volatility read from the shared runtime-history file
// (config.HistoryPath). It is a pure file read plus the Wilson-score compute via the shared
// volatility.BuildReport: no shell-out, no workspace graph, so it works even when the service
// holds no Magus. A missing or unset history file yields an empty report carrying just the
// configured threshold. Insight folds it into InsightView rather than exposing a standalone
// route; the MCP/CLI path reads magus.Magus.Volatility, not this.
func (s *Service) volatility(ctx context.Context) (types.VolatilityReport, error) {
	return volatility.BuildReport(ctx, s.config.HistoryPath, s.volatilityConfig())
}

// volatilityConfig mirrors magus.Magus.volatilityConfig (which is unexported) so the console can
// score history without reaching into the root package: the same Volatility config fields drive both.
func (s *Service) volatilityConfig() volatility.Config {
	return volatility.Config{
		Enabled:          s.config.Volatility.Enabled,
		BootstrapSamples: s.config.Volatility.BootstrapSamples,
		MinSamples:       s.config.Volatility.MinSamples,
		Threshold:        s.config.Volatility.Threshold,
	}
}
