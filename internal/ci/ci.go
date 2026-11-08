// Package ci computes provider-agnostic fan-out plans for CI systems from a
// magus workspace. CI system wrappers translate the output into their own
// matrix/pipeline format.
package ci

import (
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types"
)

const (
	defaultMaxShards = 8
	hardCeiling      = 256 // hard per-workflow matrix-job ceiling
)

// ErrInvalidMaxShards is returned when WithMaxShards receives a value of 0 or
// any negative integer other than -1 (the sentinel for "unlimited").
var ErrInvalidMaxShards = errors.New("ci: max shards must be -1 (unlimited) or a positive integer")

// Shard is one runner's worth of work; ID is its stage and letters, e.g. 1a.
type Shard struct {
	ID    string
	Units []types.Unit
}

// Plan is the output of Build: shards for a CI matrix plus source label and concurrency cap.
type Plan struct {
	// StageZero runs before Shards and computes the prerequisites Shards
	// share; see [forecast.Model.PlanStages]. Its IDs start with 0, and
	// those of Shards with 1. Empty without a model, or when no shard
	// shares one.
	StageZero   []Shard
	Shards      []Shard
	Source      string
	MaxParallel int // 0 = no cap (renderers emit max-parallel = len(Shards))
	// Fallbacks says each limit Build applied that was not asked for.
	Fallbacks []types.Fallback
}

type config struct {
	maxShards int // -1 = unlimited (up to hardCeiling)
	model     *forecast.Model
}

// Option mutates the Build configuration.
type Option func(*config)

// WithModel enables adaptive sharding; nil clears it (ceil-division).
func WithModel(m *forecast.Model) Option {
	return func(c *config) {
		c.model = m
	}
}

// WithMaxShards sets the shard limit; -1 = unlimited (capped at 256); 0 or other negatives return ErrInvalidMaxShards.
func WithMaxShards(n int) Option {
	return func(c *config) {
		c.maxShards = n
	}
}

// Build partitions units into at most maxShards shards via ceil-division
// (or a model). A shard never holds less than one unit, so the
// limit clamps to len(units).
func Build(units []types.Unit, source string, opts ...Option) (Plan, error) {
	cfg := &config{maxShards: defaultMaxShards}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.maxShards == 0 || cfg.maxShards < -1 {
		return Plan{}, ErrInvalidMaxShards
	}

	if len(units) == 0 {
		return Plan{Source: source}, nil
	}

	maxShards := cfg.maxShards
	if maxShards == -1 {
		maxShards = len(units)
	}
	var fallbacks []types.Fallback
	if maxShards > hardCeiling && len(units) > hardCeiling {
		fallbacks = append(fallbacks, types.Fallback{
			What:   fmt.Sprintf("At most %d shards, not the %d asked for", hardCeiling, maxShards),
			Why:    fmt.Sprintf("%d is the most jobs one matrix holds", hardCeiling),
			Effect: "Each shard holds more targets and runs longer than the cap asked for.",
		})
	}
	maxShards = min(maxShards, hardCeiling, len(units))

	stages := forecast.Stages{One: ceilDivide(units, maxShards)}
	if cfg.model != nil {
		stages = cfg.model.PlanStages(units, maxShards)
	}
	zero := slices.Concat(stages.Zero...)
	if err := placedOnce(zero, stages.Zero, maxShards); err != nil {
		return Plan{}, fmt.Errorf("stage zero: %w", err)
	}
	one := slices.DeleteFunc(slices.Clone(units), func(u types.Unit) bool { return slices.Contains(zero, u) })
	if err := placedOnce(one, stages.One, maxShards); err != nil {
		return Plan{}, err
	}
	if cfg.model != nil {
		if err := uncrossed(cfg.model.Replaying(zero), one); err != nil {
			return Plan{}, err
		}
	}
	return Plan{
		StageZero: numbered(0, stages.Zero),
		Shards:    numbered(1, stages.One),
		Source:    source,
		Fallbacks: fallbacks,
	}, nil
}

// uncrossed refuses a stage one whose units need another project's computing
// prerequisite (MGS3054). f replays what stage zero computed.
func uncrossed(f forecast.Model, one []types.Unit) error {
	crossed := f.Crossing(one)
	if len(crossed) == 0 {
		return nil
	}
	names := make([]string, len(crossed))
	for i, u := range crossed {
		names[i] = u.String()
	}
	return types.DiagnosticErrorf(types.ShardCrossesProjects,
		"stage one computes %s beside a unit of another project that needs it; stage zero must compute it",
		strings.Join(names, ", "))
}

// numbered is assignments as the shards of stage. An ID is the stage numeral
// and then the shard's letters, as an ordered list numbers its second level.
// The numeral keeps one --shard value unique across stages.
func numbered(stage int, assignments [][]types.Unit) []Shard {
	if len(assignments) == 0 {
		return nil
	}
	shards := make([]Shard, len(assignments))
	for i, us := range assignments {
		shards[i] = Shard{ID: strconv.Itoa(stage) + letters(i, len(assignments)), Units: us}
	}
	return shards
}

// letters is index i of n as lowercase letters, a for 0. Every index of n
// takes the same width, aa to zz past 26, so IDs sort in shard order.
func letters(i, n int) string {
	width := 1
	for capacity := 26; capacity < n; capacity *= 26 {
		width++
	}
	b := make([]byte, width)
	for j := width - 1; j >= 0; j-- {
		b[j] = byte('a' + i%26)
		i /= 26
	}
	return string(b)
}

// placedOnce checks that assignments hold every unit exactly once, in at most
// maxShards non-empty shards. A partitioner that drops or repeats a unit
// would gate less, or run twice, with nothing to show it.
func placedOnce(units []types.Unit, assignments [][]types.Unit, maxShards int) error {
	if len(assignments) > maxShards {
		return fmt.Errorf("ci: the partitioner opened %d shards, over the limit of %d", len(assignments), maxShards)
	}
	want := make(map[types.Unit]bool, len(units))
	for _, u := range units {
		want[u] = true
	}
	placed := make(map[types.Unit]int, len(units))
	for i, shard := range assignments {
		if len(shard) == 0 {
			return fmt.Errorf("ci: the partitioner left shard %d empty", i)
		}
		for _, u := range shard {
			switch {
			case !want[u]:
				return fmt.Errorf("ci: the partitioner placed %s, which is not a unit of the plan", u)
			case placed[u] > 0:
				return fmt.Errorf("ci: the partitioner placed %s on shards %d and %d", u, placed[u]-1, i)
			}
			placed[u] = i + 1
		}
	}
	for _, u := range units {
		if placed[u] == 0 {
			return fmt.Errorf("ci: the partitioner dropped %s", u)
		}
	}
	return nil
}

// ceilDivide partitions units into at most maxShards shards via ceil-division.
func ceilDivide(units []types.Unit, maxShards int) [][]types.Unit {
	if len(units) == 0 || maxShards < 1 {
		return nil
	}
	maxShards = min(maxShards, len(units))
	out := make([][]types.Unit, maxShards)
	n := len(units)
	base := n / maxShards
	extra := n % maxShards
	idx := 0
	for i := range out {
		count := base
		if i < extra {
			count++
		}
		out[i] = units[idx : idx+count]
		idx += count
	}
	return out
}
