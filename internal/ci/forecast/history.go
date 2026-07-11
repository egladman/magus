// CACHE-SAFETY NOTICE
//
// History is serialised to JSON and cached in GitHub Actions (or any other CI
// cache backend). The schema is intentionally locked to integer timing data and
// workspace-relative project paths. The following fields and their types are the
// COMPLETE allowed set:
//
//	History:      version(int), updated_at(time), constants(Constants),
//	              projects(map[string]map[string]Stats), setup([]int64),
//	              alpha([]int64), workspace_fallback_ms(int64)
//	Constants:    setup_p50_ms(int64), alpha_ms(int64)
//	Stats:        p75_ms(int64), samples(int), last_updated(time), recent([]int64),
//	              buckets(map[string]BucketStats), hit_count(int), miss_count(int),
//	              hit_rate(float64), pass_count(int), fail_count(int),
//	              volatile_count(int), recent_outcomes([]Outcome)
//	Outcome:      result(string: "pass"|"fail"|"volatile"), affected(bool),
//	              duration_ms(int64), at(time), attempts(int)
//
// DO NOT add: source code, file contents, hashes, env vars, secrets, tokens,
// commit SHAs, branch names, PR numbers, author identity, error messages, or
// stdout/stderr captures. Any new field must be added to the allowlist in
// TestHistorySchemaLock (history_test.go) with an explanation of why it is safe
// to store in a shared cache.

package forecast

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/file"
)

// HistoryVersion is the on-disk schema version.
// v4 adds PassCount/FailCount/VolatileCount/RecentOutcomes; all prior versions load cleanly.
const HistoryVersion = 4

// SampleWindow is the rolling window for duration percentiles (100 runs ≈ 5 CI days).
const SampleWindow = 100

// HitWindow is the rolling window for hit/miss counts; smaller so hit rates adapt faster.
const HitWindow = 50

// OutcomeWindow is the maximum per-run outcomes retained for volatility scoring.
const OutcomeWindow = 100

// hitColdStart is the minimum hit+miss observations before PredictDuration applies the hit-rate discount.
const hitColdStart = 5

// Default scheduling constants used until History.Constants is fitted from real observations.
const (
	DefaultSetupMs    Millis = 30_000
	DefaultAlphaMs    Millis = 5_000
	DefaultDurationMs int64  = 60_000 // per-project fallback when no history
)

// Millis is a duration expressed as whole milliseconds. Using a named type
// for the scheduling-cost parameters prevents mixing up the positional int64
// arguments. JSON marshalling uses the underlying int64.
type Millis int64

// History is the rolling store of per-project/per-target durations and scheduling constants.
type History struct {
	Version             int                         `json:"version"`
	UpdatedAt           time.Time                   `json:"updated_at"`
	Constants           Constants                   `json:"constants"`
	Projects            map[string]map[string]Stats `json:"projects"`
	Setup               []int64                     `json:"setup"`                 // shard setup times for SetupP50Ms fitting
	Alpha               []int64                     `json:"alpha"`                 // scheduling penalty observations
	WorkspaceFallbackMs int64                       `json:"workspace_fallback_ms"` // workspace-wide p75 for new projects
}

// Constants are the fitted scheduling-cost parameters: per-shard fixed cost and per-added-shard penalty.
type Constants struct {
	SetupP50Ms Millis `json:"setup_p50_ms"`
	AlphaMs    Millis `json:"alpha_ms"`
}

// BucketStats is the rolling percentile store for one workload bucket (e.g. "direct.src").
type BucketStats struct {
	P75Ms     int64   `json:"p75_ms"`
	Samples   int     `json:"samples"`
	Recent    []int64 `json:"recent"`
	HitCount  int     `json:"hit_count"`
	MissCount int     `json:"miss_count"`
	HitRate   float64 `json:"hit_rate"`
}

// Stats is one (project, target) rolling store: duration percentiles (v1+), hit rates (v3+), volatility (v4+).
type Stats struct {
	P75Ms       int64                  `json:"p75_ms"`
	Samples     int                    `json:"samples"`
	LastUpdated time.Time              `json:"last_updated"`
	Recent      []int64                `json:"recent"`
	Buckets     map[string]BucketStats `json:"buckets,omitempty"`
	HitCount    int                    `json:"hit_count"`
	MissCount   int                    `json:"miss_count"`
	HitRate     float64                `json:"hit_rate"`
	// v4+: volatility tracking
	PassCount      int       `json:"pass_count,omitempty"`
	FailCount      int       `json:"fail_count,omitempty"`
	VolatileCount  int       `json:"volatile_count,omitempty"`
	RecentOutcomes []Outcome `json:"recent_outcomes,omitempty"`
}

// Outcome is one recorded test-run result; result is "pass", "fail", or "volatile".
type Outcome struct {
	Result         string    `json:"result"`
	AffectedByDiff bool      `json:"affected"`
	DurationMs     int64     `json:"duration_ms"`
	At             time.Time `json:"at"`
	Attempts       int       `json:"attempts,omitempty"`
}

// PredictDuration returns the predicted runtime for (project, target, tags), scaled by cache-hit probability.
// Resolution: subdir bucket → generic bucket → project p75 → workspace p75 → DefaultDurationMs.
func (h *History) PredictDuration(project, target string, tags []string) time.Duration {
	p75, hitCount, missCount, hitRate := h.resolvePrediction(project, target, tags)
	if hitCount+missCount >= hitColdStart && hitRate > 0 {
		expected := float64(p75) * (1.0 - hitRate)
		if expected < 1 {
			return time.Millisecond
		}
		return time.Duration(math.Round(expected)) * time.Millisecond
	}
	return time.Duration(p75) * time.Millisecond
}

// resolvePrediction returns (p75, hitCount, missCount, hitRate) for the best matching tier.
func (h *History) resolvePrediction(project, target string, tags []string) (p75 int64, hitCount, missCount int, hitRate float64) {
	if targets, ok := h.Projects[project]; ok {
		if s, ok := targets[target]; ok {
			// Tier 1: most-specific subdir bucket (sorted for determinism).
			if len(tags) > 0 && len(s.Buckets) > 0 {
				subdirTags := make([]string, 0, len(tags))
				for _, t := range tags {
					if strings.HasPrefix(t, "direct.") {
						subdirTags = append(subdirTags, t)
					}
				}
				slices.Sort(subdirTags)
				for _, t := range subdirTags {
					if b, ok := s.Buckets[t]; ok && b.Samples >= 3 && b.P75Ms > 0 {
						return b.P75Ms, b.HitCount, b.MissCount, b.HitRate
					}
				}
				// Tier 2: generic direct/transitive bucket.
				for _, t := range tags {
					if t == "direct" || t == "transitive" {
						if b, ok := s.Buckets[t]; ok && b.Samples >= 3 && b.P75Ms > 0 {
							return b.P75Ms, b.HitCount, b.MissCount, b.HitRate
						}
					}
				}
			}
			// Tier 3: project-wide p75.
			if s.Samples >= 3 && s.P75Ms > 0 {
				return s.P75Ms, s.HitCount, s.MissCount, s.HitRate
			}
		}
	}
	if h.WorkspaceFallbackMs > 0 {
		return h.WorkspaceFallbackMs, 0, 0, 0
	}
	return DefaultDurationMs, 0, 0, 0
}

// effectiveConstants returns Constants with built-in defaults applied.
func (h *History) effectiveConstants() Constants {
	c := h.Constants
	if c.SetupP50Ms <= 0 {
		c.SetupP50Ms = DefaultSetupMs
	}
	if c.AlphaMs <= 0 {
		c.AlphaMs = DefaultAlphaMs
	}
	return c
}

// Sample is one ingestable observation. Hit=true counts toward HitRate but skips duration updates.
type Sample struct {
	Project    string
	Target     string
	DurationMs int64
	Hit        bool // cache hit: updates HitRate only, not duration percentile
	Tags       []string
}

// ShardSample is one full-shard observation used to fit scheduling constants.
type ShardSample struct {
	SetupMs int64
	TotalMs int64
	WorkMs  int64
	NShards int
}

// Update folds project and shard samples into h, recomputing percentiles, hit rates, and workspace fallback.
func (h *History) Update(now time.Time, projectSamples []Sample, shardSamples []ShardSample) {
	if h.Projects == nil {
		h.Projects = make(map[string]map[string]Stats)
	}

	for _, s := range projectSamples {
		if s.Project == "" || s.Target == "" {
			continue
		}
		if !s.Hit && s.DurationMs <= 0 {
			continue
		}
		targets, ok := h.Projects[s.Project]
		if !ok {
			targets = make(map[string]Stats)
			h.Projects[s.Project] = targets
		}
		st := targets[s.Target]

		if s.Hit {
			advanceHitWindow(&st.HitCount, &st.MissCount, true)
			st.HitRate = hitRate(st.HitCount, st.MissCount)
			if len(s.Tags) > 0 {
				if st.Buckets == nil {
					st.Buckets = make(map[string]BucketStats)
				}
				for _, tag := range s.Tags {
					b := st.Buckets[tag]
					advanceHitWindow(&b.HitCount, &b.MissCount, true)
					b.HitRate = hitRate(b.HitCount, b.MissCount)
					st.Buckets[tag] = b
				}
			}
		} else {
			advanceHitWindow(&st.HitCount, &st.MissCount, false)
			st.HitRate = hitRate(st.HitCount, st.MissCount)
			st.Recent = appendCapped(st.Recent, s.DurationMs)
			st.Samples++
			st.LastUpdated = now
			st.P75Ms = percentile(st.Recent, 0.75)
			if len(s.Tags) > 0 {
				if st.Buckets == nil {
					st.Buckets = make(map[string]BucketStats)
				}
				for _, tag := range s.Tags {
					b := st.Buckets[tag]
					advanceHitWindow(&b.HitCount, &b.MissCount, false)
					b.HitRate = hitRate(b.HitCount, b.MissCount)
					b.Recent = appendCapped(b.Recent, s.DurationMs)
					b.Samples++
					b.P75Ms = percentile(b.Recent, 0.75)
					st.Buckets[tag] = b
				}
			}
		}
		targets[s.Target] = st
	}

	for _, ss := range shardSamples {
		if ss.SetupMs > 0 {
			h.Setup = appendCapped(h.Setup, ss.SetupMs)
		}
		if ss.TotalMs > 0 && ss.WorkMs > 0 && ss.NShards > 0 {
			// α ≈ (T_total - T_setup - W/N) / N
			setup := h.Constants.SetupP50Ms
			if setup <= 0 {
				setup = DefaultSetupMs
			}
			residual := ss.TotalMs - int64(setup) - (ss.WorkMs / int64(ss.NShards))
			if residual > 0 {
				alpha := residual / int64(ss.NShards)
				if alpha > 0 {
					h.Alpha = appendCapped(h.Alpha, alpha)
				}
			}
		}
	}

	if len(h.Setup) > 0 {
		h.Constants.SetupP50Ms = Millis(percentile(h.Setup, 0.50))
	}
	if len(h.Alpha) > 0 {
		h.Constants.AlphaMs = Millis(percentile(h.Alpha, 0.50))
	}

	all := make([]int64, 0, len(h.Projects)*4)
	for _, targets := range h.Projects {
		for _, st := range targets {
			all = append(all, st.Recent...)
		}
	}
	if len(all) > 0 {
		h.WorkspaceFallbackMs = percentile(all, 0.75)
	}

	h.Version = HistoryVersion
	h.UpdatedAt = now
}

// advanceHitWindow increments the hit or miss counter, capped at HitWindow using eviction of the opposite type.
func advanceHitWindow(hitCount, missCount *int, hit bool) {
	if hit {
		*hitCount++
	} else {
		*missCount++
	}
	if *hitCount+*missCount > HitWindow {
		// Evict the opposite type to preserve the new observation's signal.
		if hit && *missCount > 0 {
			*missCount--
		} else if !hit && *hitCount > 0 {
			*hitCount--
		} else if hit {
			*hitCount-- // window already all hits; cap in place
		} else {
			*missCount-- // window already all misses; cap in place
		}
	}
}

// hitRate returns HitCount/(HitCount+MissCount), or 0 when total is zero.
func hitRate(hitCount, missCount int) float64 {
	if total := hitCount + missCount; total > 0 {
		return float64(hitCount) / float64(total)
	}
	return 0
}

// appendCapped appends v to xs and trims to the most recent SampleWindow entries.
func appendCapped(xs []int64, v int64) []int64 {
	xs = append(xs, v)
	if len(xs) > SampleWindow {
		xs = xs[len(xs)-SampleWindow:]
	}
	return xs
}

// percentile returns the p-th percentile via linear interpolation (type-7 / numpy default). Returns 0 for empty input.
func percentile(xs []int64, p float64) int64 {
	if len(xs) == 0 {
		return 0
	}
	sorted := append([]int64(nil), xs...)
	slices.Sort(sorted)

	if p <= 0 {
		return sorted[0]
	}
	if p >= 1 {
		return sorted[len(sorted)-1]
	}
	rank := p * float64(len(sorted)-1)
	lo := int(math.Floor(rank))
	hi := int(math.Ceil(rank))
	if lo == hi {
		return sorted[lo]
	}
	frac := rank - float64(lo)
	return sorted[lo] + int64(math.Round(frac*float64(sorted[hi]-sorted[lo])))
}

// Merge folds other into h, per (project, target): the entry with the newer
// LastUpdated wins. This combines the per-shard history files of one CI run —
// shards run disjoint project sets (so most entries don't collide), and where they
// do (a shared restored base), the shard that actually ran the project carries the
// freshest stats. Workspace-level fields (scheduling constants, fallback, and the
// Setup/Alpha observation windows) are taken from whichever history was updated
// most recently rather than concatenated, since shards share that base.
func (h *History) Merge(other *History) {
	if other == nil {
		return
	}
	if h.Projects == nil {
		h.Projects = make(map[string]map[string]Stats)
	}
	for project, targets := range other.Projects {
		dst, ok := h.Projects[project]
		if !ok {
			dst = make(map[string]Stats)
			h.Projects[project] = dst
		}
		for target, st := range targets {
			if cur, ok := dst[target]; !ok || st.LastUpdated.After(cur.LastUpdated) {
				dst[target] = st
			}
		}
	}
	if h.Version == 0 {
		h.Version = other.Version
	}
	if other.UpdatedAt.After(h.UpdatedAt) {
		h.UpdatedAt = other.UpdatedAt
		h.Constants = other.Constants
		h.WorkspaceFallbackMs = other.WorkspaceFallbackMs
		if len(other.Setup) > len(h.Setup) {
			h.Setup = other.Setup
		}
		if len(other.Alpha) > len(h.Alpha) {
			h.Alpha = other.Alpha
		}
	}
}

// Load reads the history file at path; a missing file is not an error (returns a zero History).
func (h *History) Load(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			*h = History{Version: HistoryVersion}
			return nil
		}
		return fmt.Errorf("forecast: read history %q: %w", path, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := codec.Unmarshal(b, h); err != nil {
		return fmt.Errorf("forecast: decode history %q: %w", path, err)
	}
	if h.Version > HistoryVersion {
		return fmt.Errorf("forecast: unsupported history version %d in %q (max %d)",
			h.Version, path, HistoryVersion)
	}
	if h.Projects == nil {
		h.Projects = make(map[string]map[string]Stats)
	}
	return nil
}

// Save writes h to path atomically.
func (h *History) Save(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	b, err := codec.MarshalIndent(h, "", "  ")
	if err != nil {
		return fmt.Errorf("forecast: encode history: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.WriteFileAtomic(path, b, 0o644); err != nil {
		return fmt.Errorf("forecast: write history: %w", err)
	}
	return nil
}
