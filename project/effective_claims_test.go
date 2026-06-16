package project

import (
	"slices"
	"testing"

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

	jsonClaims := EffectiveClaims(p, 0)
	if len(jsonClaims) != 0 {
		t.Errorf("idx=0 (json) effective claims = %v; want empty (ts outranks by order)", jsonClaims)
	}

	tsClaims := EffectiveClaims(p, 1)
	want := []string{"**/*.json", "**/*.ts"}
	if !slices.Equal(tsClaims, want) {
		t.Errorf("idx=1 (ts) effective claims = %v; want %v", tsClaims, want)
	}
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

	tsClaims := EffectiveClaims(p, 0)
	wantTS := []string{"**/*.json", "**/*.ts"}
	if !slices.Equal(tsClaims, wantTS) {
		t.Errorf("ts effective claims = %v; want %v", tsClaims, wantTS)
	}

	jsonClaims := EffectiveClaims(p, 1)
	if len(jsonClaims) != 0 {
		t.Errorf("json effective claims = %v; want empty (ts outranks by weight)", jsonClaims)
	}
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

	got := EffectiveClaims(p, 0)
	want := []string{"**/*.ts"}
	if !slices.Equal(got, want) {
		t.Errorf("effective claims = %v; want %v", got, want)
	}
}
