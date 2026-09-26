package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRiskReportLines pins the per-path rendering every sized or skipped gate prints:
// the path, its tier, its class and the fact behind it, and a gate step's own reasons
// under the step's name.
func TestRiskReportLines(t *testing.T) {
	rep := RiskReport{
		Tier: RiskScoped,
		Evidence: []RiskEvidence{
			{Path: "docs/x.md", Class: "prose", Tier: RiskTrivial, Why: `matches "**/*.md" (built-in default)`},
			{Path: "a/a.go", Class: "code", Tier: RiskScoped, Why: "Go package fx/a", Packages: []string{"fx/a"}},
			{Project: ".", Target: "buzz-test", Tier: RiskScoped, Why: "skipped"},
			{Tier: RiskFull, Why: "module libs/x is no project's root"},
		},
		Gate: []RiskGateStep{
			{Target: "lint", Projects: []string{"."}, Argv: []string{"magus", "run", "lint", ".", "--no-default-charms"}},
			{Target: "test", Projects: []string{"."}, Op: "go::go-test", Packages: []string{"fx/a"},
				Argv: []string{"magus", "run", "test", ".", "--no-default-charms"}},
		},
	}
	assert.Equal(t, []string{
		`docs/x.md: trivial (prose: matches "**/*.md" (built-in default))`,
		"a/a.go: scoped (code: Go package fx/a)",
		". buzz-test: scoped (skipped)",
		"full (module libs/x is no project's root)",
	}, rep.Lines())
	assert.Equal(t, []string{
		"magus run lint . --no-default-charms",
		"magus run test . --no-default-charms",
	}, rep.Commands())
	assert.Empty(t, RiskReport{}.Lines())
	assert.Empty(t, RiskReport{}.Commands())
}
