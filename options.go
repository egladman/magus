package magus

import (
	"context"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
)

// WithJobReader has the guard's lease rules read jobs from r in place of
// the workspace's job store: a Go program embedding magus brings its own tasks
// as jobs. The lease a call puts on its context (types.WithJobLease) names the
// job r answers for. Open fails on a nil r.
func WithJobReader(r types.JobReader) Option {
	return func(o workspace.Options) { l := o.Loading(); l.JobReader, l.JobReaderGiven = r, true }
}

// WithStepSpecs declares each kind of step Judge is asked about; a step no
// spec covers is denied. Open fails on a malformed spec, and on a spec that
// is not inert when no built-in rule, StepRule or magus\guard.step rule
// for its kind judges it. Repeated options add specs.
func WithStepSpecs(specs ...agent.StepSpec) Option {
	return sdkOption("WithStepSpecs", func(o *options) { o.stepSpecs = append(o.stepSpecs, specs...) })
}

// StepRule judges a step Judge was asked about, after the built-in rules
// and before the workspace's magus\guard.step rule for its kind.
// It is called for every step, whatever its kind, and the strictest
// decision wins. An error means it judged nothing, and Judge returns it.
// A verdict reports under step:<rule>, or step:<kind> with no rule named.
type StepRule func(ctx context.Context, req agent.StepRequest) (types.GuardVerdict, error)

// WithStepRule adds rule to the ones Judge runs, in the order given. Open
// fails on a nil rule.
func WithStepRule(rule StepRule) Option {
	return sdkOption("WithStepRule", func(o *options) { o.stepRules = append(o.stepRules, rule) })
}
