package magus

import (
	"context"

	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
)

// WithJobReader has the guard's lease rules read job rows from r in place of
// the workspace's job store: a Go program embedding magus brings its own tasks
// as rows. The lease a call puts on its context (types.WithJobLease) names the
// row r answers for. Open fails on a nil r.
func WithJobReader(r types.JobReader) Option {
	return func(o *workspace.Load) { o.JobReader, o.JobReaderGiven = r, true }
}

// WithStepRows declares each kind of step Judge is asked about; a step no
// row covers is denied. Open fails on a malformed row, and on a row that
// is not inert when neither a built-in rule nor a StepRule judges it.
// Repeated options add rows.
func WithStepRows(rows ...agent.StepRow) Option {
	return func(o *workspace.Load) { o.StepRows = append(o.StepRows, rows...) }
}

// StepRule judges a step Judge was asked about, after the built-in rules.
// It is called for every step, whatever its kind, and the strictest
// decision wins. An error means it judged nothing, and Judge returns it.
// A verdict reports under step:<rule>, or step:<kind> with no rule named.
type StepRule func(ctx context.Context, req agent.StepRequest) (types.GuardVerdict, error)

// WithStepRule adds rule to the ones Judge runs, in the order given. Open
// fails on a nil rule.
func WithStepRule(rule StepRule) Option {
	return func(o *workspace.Load) { o.StepRules = append(o.StepRules, rule) }
}
