package std

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/types"
)

// MagusRiskAssess backs magus\risk.assess: the tier magus proves for a change against
// target's gate, its evidence and the gate that suffices, the report `affected ci` sizes
// from. Read in-process from the workspace on ctx; MGS1022 outside one. An unknown option
// raises, since a dropped base would assess a different change than the one asked about.
func MagusRiskAssess(ctx context.Context, target string, opts map[string]any) (types.RiskReport, error) {
	ws := types.WorkspaceFromContext(ctx)
	if ws == nil {
		return types.RiskReport{}, errorNoWorkspace("risk.assess")
	}
	a, ok := ws.(types.RiskAssessor)
	if !ok {
		return types.RiskReport{}, errors.New("magus\\risk.assess: this workspace cannot assess a change")
	}
	if target == "" {
		return types.RiskReport{}, errors.New("magus\\risk.assess: target is empty, so name the gate the change is tiered against, such as ci")
	}
	o, err := riskOptions(opts)
	if err != nil {
		return types.RiskReport{}, fmt.Errorf("magus\\risk.assess: %w", err)
	}
	return a.AssessRisk(ctx, target, o)
}

// riskOptions decodes a RiskOptions object as the VM hands it over.
func riskOptions(opts map[string]any) (types.RiskOptions, error) {
	var o types.RiskOptions
	for k, v := range opts {
		switch k {
		case "base", "checkout":
			s, ok := v.(string)
			if !ok {
				return types.RiskOptions{}, fmt.Errorf("%s must be a string, got %T", k, v)
			}
			if k == "base" {
				o.Base = s
			} else {
				o.Checkout = s
			}
		case "paths":
			if v == nil {
				continue
			}
			paths, err := riskPaths(v)
			if err != nil {
				return types.RiskOptions{}, fmt.Errorf("paths: %w", err)
			}
			// A RiskOptions object always carries paths, defaulting to [], so an empty
			// list is a script that named none: the VCS diff, not an empty change.
			if len(paths) > 0 {
				o.Paths = paths
			}
		default:
			return types.RiskOptions{}, fmt.Errorf("unknown option %q (want base, paths, checkout)", k)
		}
	}
	return o, nil
}

// riskPaths is a Buzz [str] as the VM hands it over.
func riskPaths(v any) ([]string, error) {
	switch xs := v.(type) {
	case []string:
		return append([]string{}, xs...), nil
	case []any:
		out := make([]string, len(xs))
		for i, x := range xs {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("entry %d must be a string, got %T", i, x)
			}
			out[i] = s
		}
		return out, nil
	}
	return nil, fmt.Errorf("must be a list of strings, got %T", v)
}
