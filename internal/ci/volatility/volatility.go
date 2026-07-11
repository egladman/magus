// Package volatility provides Wilson-score volatility prediction and auto-retry for magus test runs.
// Failures on unaffected projects are always retried regardless of history.
package volatility

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/egladman/magus/internal/ci/forecast"
)

// contextKey is an unexported type for context values to avoid collisions.
type contextKey struct{}

// Config controls volatility-detection behaviour.
type Config struct {
	Enabled          bool    // when false, skip all retry and recording logic
	BootstrapSamples int     // outcomes below which all failures are retried unconditionally
	MinSamples       int     // minimum outcomes before Score returns a non-zero value
	Threshold        float64 // Wilson lower-bound volatility rate above which a target is retried
}

// DefaultConfig returns sensible defaults matching the plan.
func DefaultConfig() Config {
	return Config{
		Enabled:          true,
		BootstrapSamples: 20,
		MinSamples:       20,
		Threshold:        0.05,
	}
}

// RetryReason identifies why a retry was or was not issued.
type RetryReason string

const (
	ReasonBootstrap         RetryReason = "bootstrap"
	ReasonUnaffectedFailure RetryReason = "unaffected_failure"
	ReasonPredictedVolatile RetryReason = "predicted_volatile"
	ReasonSkip              RetryReason = "skip"
	ReasonDisabled          RetryReason = "disabled"
)

// Decision is the output of Decide.
type Decision struct {
	Retry  bool
	Reason RetryReason
}

func shouldRetry(s forecast.Stats, affected bool, cfg Config) Decision {
	n := len(s.RecentOutcomes)

	// Bootstrap: always retry until we have enough signal.
	if n < cfg.BootstrapSamples {
		return Decision{Retry: true, Reason: ReasonBootstrap}
	}

	// Unaffected failure: strong prior on volatility regardless of score.
	if !affected {
		return Decision{Retry: true, Reason: ReasonUnaffectedFailure}
	}

	// Predicted volatile: history says this is likely volatile.
	if score(s, cfg) >= cfg.Threshold {
		return Decision{Retry: true, Reason: ReasonPredictedVolatile}
	}

	return Decision{Retry: false, Reason: ReasonSkip}
}

// score returns the Wilson lower-bound volatility rate (z=1.96, 95% CI); 0 below MinSamples.
func score(s forecast.Stats, cfg Config) float64 {
	total := s.PassCount + s.FailCount + s.VolatileCount
	if total < cfg.MinSamples {
		return 0
	}
	p := float64(s.VolatileCount) / float64(total)
	n := float64(total)
	const z = 1.96
	z2 := z * z
	numerator := p + z2/(2*n) - z*math.Sqrt((p*(1-p)+z2/(4*n))/n)
	denominator := 1 + z2/n
	lb := numerator / denominator
	if lb < 0 {
		return 0
	}
	return lb
}

func isSuspectedRegression(s forecast.Stats, cfg Config) bool {
	if len(s.RecentOutcomes) < cfg.MinSamples {
		return false
	}
	if score(s, cfg) >= cfg.Threshold {
		return false // known volatile, not a regression
	}
	n := len(s.RecentOutcomes)
	if n < 2 {
		return false
	}
	last := s.RecentOutcomes[n-1]
	prev := s.RecentOutcomes[n-2]
	return last.Result == "fail" && last.AffectedByDiff &&
		prev.Result == "fail" && prev.AffectedByDiff
}

func lastPassTime(s forecast.Stats) time.Time {
	for i := len(s.RecentOutcomes) - 1; i >= 0; i-- {
		o := s.RecentOutcomes[i]
		if o.Result == "pass" || o.Result == "volatile" {
			return o.At
		}
	}
	return time.Time{}
}

func recordOutcome(s forecast.Stats, o forecast.Outcome) forecast.Stats {
	s.RecentOutcomes = append(s.RecentOutcomes, o)
	if len(s.RecentOutcomes) > forecast.OutcomeWindow {
		s.RecentOutcomes = s.RecentOutcomes[len(s.RecentOutcomes)-forecast.OutcomeWindow:]
	}
	pass, fail, volatileCount := 0, 0, 0
	for _, ro := range s.RecentOutcomes {
		switch ro.Result {
		case "pass":
			pass++
		case "fail":
			fail++
		case "volatile":
			volatileCount++
		}
	}
	s.PassCount = pass
	s.FailCount = fail
	s.VolatileCount = volatileCount
	return s
}

// Runtime holds per-run volatility state; safe for concurrent use.
type Runtime struct {
	mu       sync.Mutex
	history  *forecast.History
	path     string
	cfg      Config
	affected map[string]bool
}

// NewRuntime constructs a Runtime; empty affectedProjects means all projects are affected.
func NewRuntime(history *forecast.History, path string, cfg Config, affectedProjects []string) *Runtime {
	af := make(map[string]bool, len(affectedProjects))
	for _, p := range affectedProjects {
		af[p] = true
	}
	return &Runtime{
		history:  history,
		path:     path,
		cfg:      cfg,
		affected: af,
	}
}

// IsAffected reports whether projectPath is in the diff's affected set; empty set = all affected.
func (rt *Runtime) IsAffected(projectPath string) bool {
	if len(rt.affected) == 0 {
		return true
	}
	return rt.affected[projectPath]
}

// Decide returns a retry Decision for a just-failed (projectPath, target).
func (rt *Runtime) Decide(projectPath, target string, affected bool) Decision {
	if !rt.cfg.Enabled {
		return Decision{Retry: false, Reason: ReasonDisabled}
	}
	rt.mu.Lock()
	s := rt.stats(projectPath, target)
	rt.mu.Unlock()
	return shouldRetry(s, affected, rt.cfg)
}

// Record appends an outcome for (projectPath, target); call Save when the run completes.
func (rt *Runtime) Record(projectPath, target string, o forecast.Outcome) {
	if !rt.cfg.Enabled {
		return
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()

	if rt.history.Projects == nil {
		rt.history.Projects = make(map[string]map[string]forecast.Stats)
	}
	targets, ok := rt.history.Projects[projectPath]
	if !ok {
		targets = make(map[string]forecast.Stats)
		rt.history.Projects[projectPath] = targets
	}
	targets[target] = recordOutcome(targets[target], o)
}

// IsRegression reports whether (projectPath, target) shows a
// regression pattern based on the current in-memory history.
func (rt *Runtime) IsRegression(projectPath, target string) bool {
	rt.mu.Lock()
	s := rt.stats(projectPath, target)
	rt.mu.Unlock()
	return isSuspectedRegression(s, rt.cfg)
}

// Score returns the Wilson lower-bound volatility score for (projectPath, target).
func (rt *Runtime) Score(projectPath, target string) float64 {
	rt.mu.Lock()
	s := rt.stats(projectPath, target)
	rt.mu.Unlock()
	return score(s, rt.cfg)
}

// LastPassTime returns the most recent passing outcome time for (projectPath, target), or zero.
func (rt *Runtime) LastPassTime(projectPath, target string) time.Time {
	rt.mu.Lock()
	s := rt.stats(projectPath, target)
	rt.mu.Unlock()
	return lastPassTime(s)
}

// Save writes updated history to disk.
func (rt *Runtime) Save(ctx context.Context) error {
	if rt.path == "" {
		return nil
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.history.Save(ctx, rt.path)
}

// stats returns the Stats for (projectPath, target). Caller must hold mu.
func (rt *Runtime) stats(projectPath, target string) forecast.Stats {
	if rt.history.Projects == nil {
		return forecast.Stats{}
	}
	targets, ok := rt.history.Projects[projectPath]
	if !ok {
		return forecast.Stats{}
	}
	return targets[target]
}

func (rt *Runtime) Stats(projectPath, target string) forecast.Stats {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.stats(projectPath, target)
}

// Config returns the volatility configuration this runtime was built with.
func (rt *Runtime) Config() Config { return rt.cfg }

// WithRuntime stores rt in ctx so testProject can retrieve it.
func WithRuntime(ctx context.Context, rt *Runtime) context.Context {
	return context.WithValue(ctx, contextKey{}, rt)
}

// RuntimeFromContext retrieves the Runtime stored by WithRuntime; nil when absent.
func RuntimeFromContext(ctx context.Context) *Runtime {
	rt, _ := ctx.Value(contextKey{}).(*Runtime)
	return rt
}
