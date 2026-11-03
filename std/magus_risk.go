//go:build !wasm

package std

import (
	"context"
	"errors"

	"github.com/egladman/magus/types"
)

// MagusRiskAssess is the tier magus proves for a change against target's
// gate, the report `affected ci` sizes from. MGS1022 outside a workspace.
func MagusRiskAssess(ctx context.Context, target string, opts types.RiskOptions) (types.RiskReport, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.RiskReport{}, errorNoWorkspace("Change.tier")
	}
	a, ok := ws.(types.RiskAssessor)
	if !ok {
		return types.RiskReport{}, errors.New("magus\\Change.tier: this workspace cannot assess a change")
	}
	if target == "" {
		return types.RiskReport{}, errors.New("magus\\Change.tier: target is empty, so name the gate the change is tiered against, such as ci")
	}
	return a.AssessRisk(ctx, target, opts)
}
