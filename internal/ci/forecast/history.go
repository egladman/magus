// CACHE-SAFETY NOTICE
//
// History is serialized to JSON and cached in GitHub Actions (or any other CI
// cache backend). The schema is intentionally locked to integer timing data and
// workspace-relative project paths. The following fields and their types are the
// COMPLETE allowed set:
//
//	History:      version(int), updated_at(time), constants(Constants),
//	              projects(map[string]map[string]Stats), setup([]int64),
//	              alpha([]int64), workspace_fallback_ms(int64),
//	              runs([]Run)
//	Constants:    setup_p50_ms(int64), alpha_ms(int64)
//	Stats:        p75_ms(int64), samples(int), last_updated(time), recent([]int64),
//	              buckets(map[string]BucketStats), hit_count(int), miss_count(int),
//	              hit_rate(float64), pass_count(int), fail_count(int),
//	              volatile_count(int), recent_outcomes([]Outcome)
//	Outcome:      result(OutcomeResult, a string: "pass"|"fail"|"volatile"), affected(bool),
//	              duration_ms(int64), at(time), attempts(int)
//	Run:          commit(string: a commit id), ref(string), target(string),
//	              status(string: "passed"|"failed"), at(time)
//
// DO NOT add: source code, file contents, env vars, secrets, tokens, PR numbers,
// author identity, error messages, or stdout/stderr captures. Any new field must
// be added to the allowlist in TestHistorySchemaLock (history_test.go) with an
// explanation of why it is safe to store in a shared cache.
//
// COMMIT IDS AND REF NAMES are permitted, in Runs and nowhere else. A considered
// exception, not an erosion:
//
//   - There is no content-free substitute: "the commit this branch last passed at"
//     is a commit reference by definition.
//   - Neither is content. A commit id NAMES a revision without revealing anything in
//     it, unlike the file contents, secrets and captured output still refused.
//   - The blast radius is one repository: a CI cache is repository-scoped, and this
//     is NOT written to magus's shared content-addressed remote cache.
//
// A boundary, not a precedent. A field naming WHAT changed rather than WHICH revision
// is the thing this notice exists to stop.
//
// ci.record_runs turns the run log off for a workspace that will not accept even
// this; see internal/config.CI.RecordRuns.

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

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
)

// HistoryVersion is the on-disk schema version.
// v4 adds PassCount/FailCount/VolatileCount/RecentOutcomes; all prior versions load cleanly.
// v5 adds Runs, the per-branch log of end-to-end run outcomes.
const HistoryVersion = 5

// SampleWindow is the rolling window for duration percentiles (100 runs ≈ 5 CI days).
const SampleWindow = 100

// HitWindow is the rolling window for hit/miss counts; smaller so hit rates adapt faster.
const HitWindow = 50

// OutcomeWindow is the maximum per-run outcomes retained for volatility scoring.
const OutcomeWindow = 100

// hitColdStart is the minimum hit+miss observations before PredictDuration applies the hit-rate discount.
const hitColdStart = 5

// Default scheduling constants used until History.Constants is fitted from real
// observations - which is currently never: nothing in this tree calls Update with
// shardSamples, so SetupP50Ms and AlphaMs stay these defaults forever. The raw
// numbers Update would fit from exist - report.ShardTotal, written by
// (*magus.ReportWriter).RecordShardTotal in a CI matrix run (see report.go) - but
// nothing reads that JSONL stream back into a History to close the loop.
const (
	DefaultSetupMs    Millis = 30_000
	DefaultAlphaMs    Millis = 5_000
	DefaultDurationMs int64  = 60_000 // per-project fallback when no history
)

// Millis is a duration expressed as whole milliseconds. Using a named type
// for the scheduling-cost parameters prevents mixing up the positional int64
// arguments. JSON marshaling uses the underlying int64.
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
	// Runs is the rolling log of end-to-end runs, oldest first. See [Run]; query it
	// with [History.PassedCommit].
	Runs []Run `json:"runs,omitempty"`
}

// Run is one recorded end-to-end run of a target on a branch: what it ran at, how it
// came out, and when.
//
// A LOG rather than counters. PassCount/FailCount answer "how often does this target
// flap", a volatility question; a tally cannot answer anything about a PARTICULAR run.
// This can: which commit was verified, which broke it, how far back the last passing
// one is. "Anything merged by a run that did not pass" is not derivable from two
// integers, and it is what an affected diff has to know.
//
// Ref and Target are ON the run rather than keys above it, so a Run says what it is
// without the caller carrying the key it was filed under. One flat window is enough
// because the only recording caller is a run's aggregation step.
//
// Ref, not Branch: it is types.VCSMeta.Ref, the movable name a backend points at the
// current revision (git branch, hg named branch, jj bookmark), and it is what
// vcs.ref() already hands a magusfile.
//
// Target is recorded rather than assumed. A workspace whose pipeline anchor is not
// `ci` would otherwise read a run left by a different pipeline and treat a commit as
// verified by a gate that never looked at it.
type Run struct {
	Commit string    `json:"commit"`
	Ref    string    `json:"ref"`
	Target string    `json:"target"`
	Status RunStatus `json:"status"`
	At     time.Time `json:"at"`
}

// RunStatus is how a run came out. "Passed"/"failed" rather than green/red or
// success/failure: it is the word magus's own console prints ([pass], [fail]) and the
// one Stats already counts with.
type RunStatus string

const (
	RunPassed RunStatus = "passed"
	RunFailed RunStatus = "failed"
)

// RunWindow is the rolling window of runs kept. Matched to SampleWindow
// (roughly 5 CI days) because it answers the same kind of question over the same span,
// and because the useful reads here - the last passing commit, the current red streak -
// are all near the head of the log.
const RunWindow = 100

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

// OutcomeResult is one recorded run's verdict. A named string type rather than a bare
// one because the value was compared against literals at six sites: a typo in any of them
// matched nothing and silently zeroed every volatility score, which is a wrong answer that
// looks exactly like a healthy one. The underlying type stays string, so the cached JSON
// is byte-identical to what earlier versions wrote.
type OutcomeResult string

const (
	// OutcomePass is a target that succeeded on its first attempt.
	OutcomePass OutcomeResult = "pass"
	// OutcomeFail is a target that failed and stayed failed.
	OutcomeFail OutcomeResult = "fail"
	// OutcomeVolatile is a target that failed and then passed on a retry.
	OutcomeVolatile OutcomeResult = "volatile"
)

// Outcome is one recorded test-run result.
type Outcome struct {
	Result         OutcomeResult `json:"result"`
	AffectedByDiff bool          `json:"affected"`
	DurationMs     int64         `json:"duration_ms"`
	At             time.Time     `json:"at"`
	Attempts       int           `json:"attempts,omitempty"`
	// MaxRSSBytes is the target's peak resident memory, the maximum over every
	// process it ran. Omitted when unknown, and unknown is the honest default:
	// the platforms that cannot report it and every history file written before
	// this field existed both decode to zero.
	//
	// Zero therefore means UNMEASURED, never "used nothing". A consumer that
	// treats it as a small number will read the targets it knows least about as
	// the cheapest ones to co-schedule, which is precisely backwards.
	MaxRSSBytes int64 `json:"max_rss_bytes,omitempty"`
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
//
// Tier 3 is the tier the record path fills. Tiers 1-2 read Buckets, keyed by tags Tags()
// derives from the changed-file list - known at plan time, not at record time. Filling
// them means threading the affected set onto the outcome, a real change to what the
// record path knows.
//
// HitCount/MissCount stay unfed for the same shape of reason: a cache hit executes no
// target, so no outcome exists. hitRate stays 0 and no hit discount applies, which
// over-predicts - the safe direction for a shard planner.
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

// ApplyDuration folds one measured run into a target's duration statistics.
//
// Exported because the record path lives in internal/ci/volatility, which sees every
// executed target, while Update ingests a batch nobody builds. The two were wired to
// different fields: run.go measured a duration into Outcome.DurationMs, nothing read it,
// and resolvePrediction returned DefaultDurationMs forever, so LPT packed uniform
// weights. One definition of "duration to p75" now, so they cannot drift apart again.
//
// at is what Merge resolves collisions on. Left zero, per-shard histories compared equal
// and merge-history kept whichever file it read first.
func ApplyDuration(st Stats, durationMs int64, at time.Time) Stats {
	if durationMs <= 0 {
		return st // an unmeasured run is not a sample; a zero would drag p75 down
	}
	st.Recent = appendCapped(st.Recent, durationMs)
	st.Samples++
	st.LastUpdated = at
	st.P75Ms = percentile(st.Recent, 0.75)
	return st
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
			st = ApplyDuration(st, s.DurationMs, now)
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
	// Runs union rather than freshest-wins: two histories being merged describe
	// different runs, not two readings of one, so keeping only the newer would discard
	// the record of everything before it.
	if len(other.Runs) > 0 {
		seen := make(map[Run]struct{}, len(h.Runs))
		for _, r := range h.Runs {
			seen[r] = struct{}{}
		}
		for _, r := range other.Runs {
			if _, dup := seen[r]; dup {
				continue
			}
			seen[r] = struct{}{}
			h.Runs = append(h.Runs, r)
		}
		slices.SortStableFunc(h.Runs, func(a, b Run) int { return a.At.Compare(b.At) })
		if len(h.Runs) > RunWindow {
			h.Runs = h.Runs[len(h.Runs)-RunWindow:]
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

// RecordRun appends one end-to-end run to the log, trimming to [RunWindow].
// Only a caller that knows how the WHOLE run came out may call it - in a sharded CI
// run that is the aggregation job, never an individual shard, since no shard can see
// its siblings' results.
//
// Failures are recorded, not just passes: a pass-only log cannot distinguish "nothing
// has run on this ref" from "everything since has failed", and those want opposite
// responses.
//
// Recording a passing run whose affected set was empty is deliberate - skipping it
// would pin the last passing commit at whichever one last touched something and grow
// every later diff without bound.
func (h *History) RecordRun(run Run, now time.Time) {
	if run.Ref == "" || run.Commit == "" {
		return
	}
	if run.At.IsZero() {
		run.At = now
	}
	h.Runs = append(h.Runs, run)
	if len(h.Runs) > RunWindow {
		h.Runs = h.Runs[len(h.Runs)-RunWindow:]
	}
}

// PassedCommit returns the commit ref most recently PASSED target at, and whether
// any such run is on record. Runs of another ref or target are skipped rather than
// ending the search, so a `ci` marker is not shadowed by a later `nightly` one.
//
// A false return is the ordinary case on a fresh workspace, a fork, or after the store
// aged out. Callers must have an answer for it rather than treating it as a failure,
// and must not fall back to a base that measures nothing - which is what comparing a
// ref against itself does.
func (h *History) PassedCommit(ref, target string) (string, bool) {
	for i := len(h.Runs) - 1; i >= 0; i-- {
		r := h.Runs[i]
		if !r.matches(ref, target) || r.Status != RunPassed || r.Commit == "" {
			continue
		}
		return r.Commit, true
	}
	return "", false
}

// FailedSince returns the runs of target on ref since its last passing one, oldest
// first, and is empty when the most recent run passed. It is the "what is still
// unverified" view the affected diff implies: every commit in it is one a passing run
// never covered.
func (h *History) FailedSince(ref, target string) []Run {
	var out []Run
	for i := len(h.Runs) - 1; i >= 0; i-- {
		r := h.Runs[i]
		if !r.matches(ref, target) {
			continue
		}
		if r.Status == RunPassed {
			break
		}
		out = append([]Run{r}, out...)
	}
	return out
}

// matches reports whether r is a run of the named ref and target. An empty filter
// matches anything; an empty field on the run does too, so a record written before
// either was populated is not silently invisible.
func (r Run) matches(ref, target string) bool {
	if ref != "" && r.Ref != "" && r.Ref != ref {
		return false
	}
	return target == "" || r.Target == "" || r.Target == target
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
	if err := json.Unmarshal(b, h); err != nil {
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
	b, err := json.MarshalIndent(h, "", "  ")
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

// PredictPeakRSS returns the highest peak resident memory recorded for
// (project, target), and whether anything was recorded at all.
//
// The MAXIMUM, not a percentile. A slow run costs you the difference, so p75 is right
// for scheduling time; a run that exceeds what the machine has takes the machine down,
// so the only figure that protects a runner is the worst seen.
//
// The bool is load-bearing: every history written before peaks were recorded, every
// platform that cannot report them, and every target that has never run all decode to
// zero, and a planner reading zero as "needs nothing" would pack precisely the targets
// it knows least about onto one machine.
func (h *History) PredictPeakRSS(project, target string) (int64, bool) {
	targets, ok := h.Projects[project]
	if !ok {
		return 0, false
	}
	s, ok := targets[target]
	if !ok {
		return 0, false
	}
	var max int64
	for _, o := range s.RecentOutcomes {
		if o.MaxRSSBytes > max {
			max = o.MaxRSSBytes
		}
	}
	return max, max > 0
}
