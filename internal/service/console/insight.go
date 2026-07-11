package console

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/egladman/magus/internal/ci/flake"
	"github.com/egladman/magus/internal/ci/forecast"
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

// Insight assembles the four VCS-history lenses (hotspots, affinity, ownership, trend) from
// one bounded git-log scan, computed in-daemon via the held *magus.Magus. The result is
// cached for insightTTL so a burst of dashboard polls does not re-shell git on every
// request; the cache is refreshed on the first call after the TTL lapses. Assembly is
// serialized by the cache mutex, so concurrent pollers past a cold TTL wait for one scan
// rather than each launching their own.
func (s *Service) Insight(ctx context.Context) (types.InsightView, error) {
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

// Flake reports per-(project, target) flakiness read from the shared runtime-history file
// (config.HistoryPath). It is a pure file read plus the Wilson-score compute: no shell-out,
// no workspace graph, so it works even when the service holds no Magus. A missing or unset
// history file yields an empty report carrying just the configured threshold. Targets are
// sorted by (project, target) for a deterministic response body.
func (s *Service) Flake(ctx context.Context) (types.FlakeReport, error) {
	cfg := s.flakeConfig()
	report := types.FlakeReport{Threshold: cfg.Threshold}

	path := s.config.HistoryPath
	if path == "" {
		return report, nil
	}
	var hist forecast.History
	if err := hist.Load(ctx, path); err != nil {
		return types.FlakeReport{}, err
	}
	rt := flake.NewRuntime(&hist, path, cfg, nil)
	for project, targets := range hist.Projects {
		for target := range targets {
			st := rt.Stats(project, target)
			sc := rt.Score(project, target)
			report.Targets = append(report.Targets, types.FlakeTarget{
				Project:  project,
				Target:   target,
				Score:    sc,
				Flaky:    sc >= cfg.Threshold,
				Pass:     st.PassCount,
				Fail:     st.FailCount,
				Flake:    st.FlakeCount,
				Samples:  len(st.RecentOutcomes),
				LastPass: rt.LastPassTime(project, target),
			})
		}
	}
	sort.Slice(report.Targets, func(i, j int) bool {
		if report.Targets[i].Project != report.Targets[j].Project {
			return report.Targets[i].Project < report.Targets[j].Project
		}
		return report.Targets[i].Target < report.Targets[j].Target
	})
	return report, nil
}

// flakeConfig mirrors magus.Magus.flakeConfig (which is unexported) so the console can score
// history without reaching into the root package: the same Flake config fields drive both.
func (s *Service) flakeConfig() flake.Config {
	return flake.Config{
		Enabled:          s.config.Flake.Enabled,
		BootstrapSamples: s.config.Flake.BootstrapSamples,
		MinSamples:       s.config.Flake.MinSamples,
		Threshold:        s.config.Flake.Threshold,
	}
}
