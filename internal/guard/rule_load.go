package guard

import (
	"context"

	"github.com/egladman/magus/internal/guard/steps"
	"github.com/egladman/magus/internal/workspace"
)

// ruleLoad is what one load of a root magusfile registered: the registry's
// seams, and the step rules internal/workspace cannot hold.
type ruleLoad struct {
	*workspace.WorkspaceRegistry
	steps *steps.Group
}

// within is ctx with the load's registry and step group to register into.
func (l *ruleLoad) within(ctx context.Context) context.Context {
	return steps.WithGroup(workspace.ContextWithRegistry(ctx, l.WorkspaceRegistry), l.steps)
}

// StepRule is the rule for kind, nil when l is nil or registers none.
func (l *ruleLoad) StepRule(kind string) steps.Rule {
	if l == nil {
		return nil
	}
	return l.steps.Rule(kind)
}
