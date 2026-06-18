package project

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

func makeSpell(name string, claims []string) *types.Spell {
	return types.NewSpell(name, types.WithClaims(claims...))
}

func projectFor(spells []*types.Spell, bindings []*types.Binding) *types.Project {
	return &types.Project{ResolvedSpells: spells, Bindings: bindings}
}

// TestEffectiveClaimsLastWins covers the default zero-weight case where
// later-registered spells carve out overlapping globs from earlier ones.
func TestEffectiveClaimsLastWins(t *testing.T) {
	p := projectFor(
		[]*types.Spell{
			makeSpell("json", []string{"**/*.json"}),
			makeSpell("ts", []string{"**/*.json", "**/*.ts"}),
		},
		[]*types.Binding{
			{Name: "json"},
			{Name: "ts"},
		},
	)

	assert.Empty(t, EffectiveClaims(p, 0), "idx=0 (json) effective claims should be empty (ts outranks by order)")
	assert.Equal(t, []string{"**/*.json", "**/*.ts"}, EffectiveClaims(p, 1))
}

// TestEffectiveClaimsWeightWins covers the case where an earlier-registered
// spell has a higher weight and should keep its overlapping claims.
func TestEffectiveClaimsWeightWins(t *testing.T) {
	p := projectFor(
		[]*types.Spell{
			makeSpell("ts", []string{"**/*.json", "**/*.ts"}),
			makeSpell("json", []string{"**/*.json"}),
		},
		[]*types.Binding{
			{Name: "ts", ClaimWeight: 10},
			{Name: "json"},
		},
	)

	assert.Equal(t, []string{"**/*.json", "**/*.ts"}, EffectiveClaims(p, 0))
	assert.Empty(t, EffectiveClaims(p, 1), "json effective claims should be empty (ts outranks by weight)")
}

// TestEffectiveClaimsRemovedClaimsStillApplied verifies WithoutClaim still
// works as an escape hatch on top of weighted resolution.
func TestEffectiveClaimsRemovedClaimsStillApplied(t *testing.T) {
	p := projectFor(
		[]*types.Spell{
			makeSpell("ts", []string{"**/*.json", "**/*.ts"}),
		},
		[]*types.Binding{
			{Name: "ts", RemovedClaims: []string{"**/*.json"}},
		},
	)

	assert.Equal(t, []string{"**/*.ts"}, EffectiveClaims(p, 0))
}
