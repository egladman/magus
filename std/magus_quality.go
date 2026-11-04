package std

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/types"
)

// qualityWorkspace is the part of *magus.Magus that scores and mutates.
type qualityWorkspace interface {
	Crap(ctx context.Context, base string) (types.Crap, error)
	Mutants(ctx context.Context, base string) (types.Mutants, error)
}

// qualityFromContext raises when no workspace can judge, never an empty record.
func qualityFromContext(ctx context.Context, member string) (qualityWorkspace, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return nil, errorNoWorkspace(member)
	}
	q, ok := ws.(qualityWorkspace)
	if !ok {
		return nil, fmt.Errorf("magus\\%s: this workspace cannot score functions", member)
	}
	return q, nil
}

// MagusCrap backs magus\crap: the CRAP score of each function touched since
// base. base "" scores every function in the workspace.
func MagusCrap(ctx context.Context, base string) (types.Crap, error) {
	q, err := qualityFromContext(ctx, "crap")
	if err != nil {
		return types.Crap{}, err
	}
	out, err := q.Crap(ctx, base)
	if err != nil {
		return types.Crap{}, fmt.Errorf("magus\\crap: %w", err)
	}
	return out, nil
}

// MagusMutants backs magus\mutants: the mutants of the functions touched
// since base. base "" is refused.
func MagusMutants(ctx context.Context, base string) (types.Mutants, error) {
	if base == "" {
		return types.Mutants{}, errors.New("magus\\mutants: name a base, since mutating a whole workspace is not offered")
	}
	q, err := qualityFromContext(ctx, "mutants")
	if err != nil {
		return types.Mutants{}, err
	}
	out, err := q.Mutants(ctx, base)
	if err != nil {
		return types.Mutants{}, fmt.Errorf("magus\\mutants: %w", err)
	}
	return out, nil
}
