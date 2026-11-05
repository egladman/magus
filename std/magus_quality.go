//go:build !wasm

package std

import (
	"context"
	"fmt"

	"github.com/egladman/magus/types"
)

// qualityWorkspace is the part of *magus.Magus that scores functions.
type qualityWorkspace interface {
	ChangeRisk(ctx context.Context, base string) (types.ChangeRisk, error)
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
	q, err := qualityFromContext(ctx, member)
	if err != nil {
		return types.ChangeRisk{}, err
	}
	out, err := q.ChangeRisk(ctx, base)
	if err != nil {
		return types.ChangeRisk{}, fmt.Errorf("magus\\%s: %w", member, err)
	}
	return out, nil
}
