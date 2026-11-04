//go:build !wasm

package std

import (
	"context"
	"fmt"

	"github.com/egladman/magus/types"
)

// qualityWorkspace is the part of *magus.Magus that scores and mutates.
type qualityWorkspace interface {
	ChangeRisk(ctx context.Context, base string) (types.ChangeRisk, error)
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

// MagusChangeRisk is Change.risk(): the change risk of each function
// touched since c's base.
func MagusChangeRisk(ctx context.Context, c types.Change) (types.ChangeRisk, error) {
	base, err := heldChangeBase(ctx, c, "risk")
	if err != nil {
		return types.ChangeRisk{}, err
	}
	return scoreRisk(ctx, "Change.risk", base)
}

// MagusWorkspaceRisk is Workspace.risk(): the change risk of every function
// in the workspace.
func MagusWorkspaceRisk(ctx context.Context, _ types.MagusWorkspace) (types.ChangeRisk, error) {
	return scoreRisk(ctx, "Workspace.risk", "")
}

// MagusChangeMutants is Change.mutants(): the mutants of the functions
// touched since c's base.
func MagusChangeMutants(ctx context.Context, c types.Change) (types.Mutants, error) {
	base, err := heldChangeBase(ctx, c, "mutants")
	if err != nil {
		return types.Mutants{}, err
	}
	return mutate(ctx, "Change.mutants", base)
}

// heldChangeBase is c's base once heldChange vouches for its tree.
func heldChangeBase(ctx context.Context, c types.Change, member string) (string, error) {
	ws, err := heldChange(ctx, c, member)
	if err != nil {
		return "", err
	}
	base, err := changeBase(ctx, ws, c)
	if err != nil {
		return "", fmt.Errorf("magus\\Change.%s: %w", member, err)
	}
	return base, nil
}

// scoreRisk scores the functions touched since base, or every function
// when base is "".
func scoreRisk(ctx context.Context, member, base string) (types.ChangeRisk, error) {
	return measure(ctx, member, base, qualityWorkspace.ChangeRisk)
}

func mutate(ctx context.Context, member, base string) (types.Mutants, error) {
	return measure(ctx, member, base, qualityWorkspace.Mutants)
}

func measure[T any](ctx context.Context, member, base string, by func(qualityWorkspace, context.Context, string) (T, error)) (T, error) {
	var zero T
	q, err := qualityFromContext(ctx, member)
	if err != nil {
		return zero, err
	}
	out, err := by(q, ctx, base)
	if err != nil {
		return zero, fmt.Errorf("magus\\%s: %w", member, err)
	}
	return out, nil
}
