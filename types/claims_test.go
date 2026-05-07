package types_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
)

func TestEffectiveClaimsRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		claims []string
	}{
		{"nil", nil},
		{"single", []string{"go"}},
		{"multiple", []string{"go", "python", "node"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := types.WithEffectiveClaims(context.Background(), tc.claims)
			got := types.EffectiveClaimsFromContext(ctx)
			if len(got) != len(tc.claims) {
				t.Fatalf("got %v, want %v", got, tc.claims)
			}
			for i, want := range tc.claims {
				if got[i] != want {
					t.Errorf("[%d] got %q, want %q", i, got[i], want)
				}
			}
		})
	}
}

func TestEffectiveClaimsFromContext_MissingReturnsNil(t *testing.T) {
	got := types.EffectiveClaimsFromContext(context.Background())
	if got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestEffectiveClaimsFromContext_InnerContextWins(t *testing.T) {
	outer := types.WithEffectiveClaims(context.Background(), []string{"go"})
	inner := types.WithEffectiveClaims(outer, []string{"python"})
	got := types.EffectiveClaimsFromContext(inner)
	if len(got) != 1 || got[0] != "python" {
		t.Errorf("got %v, want [python]", got)
	}
}
