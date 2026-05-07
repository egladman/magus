// Package ci computes provider-agnostic fan-out plans for CI systems from a
// magus workspace. CI system wrappers translate the output into their own
// matrix/pipeline format.
package ci

import (
	"errors"
	"fmt"

	"github.com/egladman/magus/types"
)

const (
	defaultMaxShards = 8
	hardCeiling      = 256 // hard per-workflow matrix-job ceiling
)

// ErrInvalidMaxShards is returned when WithMaxShards receives a value of 0 or
// any negative integer other than -1 (the sentinel for "unlimited").
var ErrInvalidMaxShards = errors.New("ci: max shards must be -1 (unlimited) or a positive integer")

// Shard is one runner's worth of work; ID is zero-padded so log entries sort correctly.
type Shard struct {
	ID       string
	Projects []*types.Project
}

// Plan is the output of Build: shards for a CI matrix plus source label and concurrency cap.
type Plan struct {
	Shards      []Shard
	Source      string
	MaxParallel int // 0 = no cap (renderers emit max-parallel = len(Shards))
}

// Forecaster is the optional adaptive partitioner that replaces ceil-division when supplied to Build.
// Defined by interface to avoid an import cycle with magus/ci/forecast.
type Forecaster interface {
	Plan(projects []*types.Project, maxShards int) [][]*types.Project
}

type config struct {
	maxShards  int // -1 = unlimited (up to hardCeiling)
	forecaster Forecaster
}

// Option mutates the Build configuration.
type Option func(*config)

// WithForecaster enables adaptive sharding; nil clears it (falls back to ceil-division).
func WithForecaster(f Forecaster) Option {
	return func(c *config) {
		c.forecaster = f
	}
}

// WithMaxShards sets the shard limit; -1 = unlimited (capped at 256); 0 or other negatives return ErrInvalidMaxShards.
func WithMaxShards(n int) Option {
	return func(c *config) {
		c.maxShards = n
	}
}

// Build partitions projects into at most maxShards shards via ceil-division (or a forecaster).
func Build(projects []*types.Project, source string, opts ...Option) (Plan, error) {
	cfg := &config{maxShards: defaultMaxShards}
	for _, o := range opts {
		o(cfg)
	}
	if cfg.maxShards == 0 || cfg.maxShards < -1 {
		return Plan{}, ErrInvalidMaxShards
	}

	if len(projects) == 0 {
		return Plan{Source: source}, nil
	}

	maxShards := cfg.maxShards
	if maxShards == -1 {
		// Unlimited: one shard per project, still subject to hardCeiling.
		maxShards = len(projects)
	}
	if maxShards > hardCeiling {
		maxShards = hardCeiling
	}
	if maxShards > len(projects) {
		maxShards = len(projects)
	}

	var assignments [][]*types.Project
	if cfg.forecaster != nil {
		assignments = cfg.forecaster.Plan(projects, maxShards)
		if len(assignments) == 0 { // defensive: never drop work
			assignments = ceilDivide(projects, maxShards)
		}
	} else {
		assignments = ceilDivide(projects, maxShards)
	}

	idWidth := digits(len(assignments) - 1)

	shards := make([]Shard, len(assignments))
	for i, ps := range assignments {
		shards[i] = Shard{
			ID:       fmt.Sprintf("%0*d", idWidth, i),
			Projects: ps,
		}
	}

	return Plan{Shards: shards, Source: source}, nil
}

// ceilDivide partitions projects into at most maxShards shards via ceil-division.
func ceilDivide(projects []*types.Project, maxShards int) [][]*types.Project {
	if len(projects) == 0 || maxShards < 1 {
		return nil
	}
	if maxShards > len(projects) {
		maxShards = len(projects)
	}
	out := make([][]*types.Project, maxShards)
	n := len(projects)
	base := n / maxShards
	extra := n % maxShards
	idx := 0
	for i := range out {
		count := base
		if i < extra {
			count++
		}
		out[i] = projects[idx : idx+count]
		idx += count
	}
	return out
}

// digits returns the number of decimal digits needed to represent n.
// digits(0) == 1.
func digits(n int) int {
	if n <= 0 {
		return 1
	}
	d := 0
	for n > 0 {
		d++
		n /= 10
	}
	return d
}
