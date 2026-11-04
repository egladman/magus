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
	ID    string
	Units []types.Unit
}

// Plan is the output of Build: shards for a CI matrix plus source label and concurrency cap.
type Plan struct {
	Shards      []Shard
	Source      string
	MaxParallel int // 0 = no cap (renderers emit max-parallel = len(Shards))
}

// Forecaster is the optional adaptive partitioner that replaces
// ceil-division when supplied to Build. Plan returns every unit exactly
// once, in at most maxShards shards.
type Forecaster interface {
	Plan(units []types.Unit, maxShards int) [][]types.Unit
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

// Build partitions units into at most maxShards shards via ceil-division
// (or a forecaster). A shard never holds less than one unit, so the
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
	maxShards = min(maxShards, hardCeiling, len(units))

	var assignments [][]types.Unit
	if cfg.forecaster != nil {
		assignments = cfg.forecaster.Plan(units, maxShards)
	} else {
		assignments = ceilDivide(units, maxShards)
	}
	if err := placedOnce(units, assignments, maxShards); err != nil {
		return Plan{}, err
	}

	idWidth := digits(len(assignments) - 1)

	shards := make([]Shard, len(assignments))
	for i, us := range assignments {
		shards[i] = Shard{
			ID:    fmt.Sprintf("%0*d", idWidth, i),
			Units: us,
		}
	}

	return Plan{Shards: shards, Source: source}, nil
}

// placedOnce checks that assignments hold every unit exactly once, in at most
// maxShards non-empty shards. A partitioner that drops or repeats a unit
// would gate less, or run twice, with nothing to show it.
func placedOnce(units []types.Unit, assignments [][]types.Unit, maxShards int) error {
	if len(assignments) > maxShards {
		return fmt.Errorf("ci: the partitioner opened %d shards, over the limit of %d", len(assignments), maxShards)
	}
	want := make(map[string]bool, len(units))
	for _, u := range units {
		want[u.String()] = true
	}
	placed := make(map[string]int, len(units))
	for i, shard := range assignments {
		if len(shard) == 0 {
			return fmt.Errorf("ci: the partitioner left shard %d empty", i)
		}
		for _, u := range shard {
			k := u.String()
			switch {
			case !want[k]:
				return fmt.Errorf("ci: the partitioner placed %s, which is not a unit of the plan", k)
			case placed[k] > 0:
				return fmt.Errorf("ci: the partitioner placed %s on shards %d and %d", k, placed[k]-1, i)
			}
			placed[k] = i + 1
		}
	}
	for _, u := range units {
		if placed[u.String()] == 0 {
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
