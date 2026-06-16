package types

import (
	"context"
	"testing"
)

func TestCharmsStack(t *testing.T) {
	ctx := context.Background()
	if HasCharm(ctx, "write") {
		t.Fatal("empty context should carry no charms")
	}

	// Multiple charms coexist (stacking) and order is insignificant.
	ctx = WithCharms(ctx, []string{"write", "debug"})
	if !HasCharm(ctx, "write") || !HasCharm(ctx, "debug") {
		t.Fatalf("both charms should be present, got %v", CharmsFromContext(ctx))
	}
	if HasCharm(ctx, "verbose") {
		t.Fatal("a charm that was not set must be absent")
	}

	// An empty set is a no-op and must not clobber existing charms.
	if got := WithCharms(ctx, nil); !HasCharm(got, "write") {
		t.Fatal("WithCharms(nil) must preserve existing charms")
	}
}

// TestHasCharmNormalizes documents that charm matching is case- and
// separator-insensitive on both sides: an active charm stored in one spelling
// matches a query in another, mirroring target-name normalization.
func TestHasCharmNormalizes(t *testing.T) {
	// Active charm declared with odd casing/separator; queried canonically.
	ctx := WithCharms(context.Background(), []string{"No_Cache"})
	if !HasCharm(ctx, "no-cache") {
		t.Errorf("no-cache query must match active No_Cache, got charms %v", CharmsFromContext(ctx))
	}
	// And the reverse: canonical active, odd-cased query.
	ctx = WithCharms(context.Background(), []string{"write"})
	if !HasCharm(ctx, "WRITE") {
		t.Error("WRITE query must match active write charm")
	}
}

// TestReservedCharms locks in the built-in charm set the typo guard exempts and
// the doctor collision check enumerates: recognition is casing/separator-blind,
// and ReservedCharms hands back an independent copy callers cannot mutate.
func TestReservedCharms(t *testing.T) {
	for _, name := range []string{"rw", "cd", "gha", "RW", "CD", "GHA"} {
		if !IsReservedCharm(name) {
			t.Errorf("IsReservedCharm(%q) = false, want true", name)
		}
	}
	if IsReservedCharm("container") {
		t.Error("IsReservedCharm(container) = true, want false")
	}

	got := ReservedCharms()
	want := []string{"rw", "cd", "gha"}
	if len(got) != len(want) {
		t.Fatalf("ReservedCharms() = %v, want %v", got, want)
	}
	got[0] = "mutated"
	if ReservedCharms()[0] != want[0] {
		t.Error("ReservedCharms() must return an independent copy")
	}
}

// TestParseTargetNormalizesCharms locks in that the "target:charm" suffix is
// canonicalized at the parse boundary, so everything downstream (cache key, ci
// strip, typo guard) sees one spelling.
func TestParseTargetNormalizesCharms(t *testing.T) {
	got, err := ParseTarget("format:Write,No_Cache")
	if err != nil {
		t.Fatalf("ParseTarget: %v", err)
	}
	want := []string{"write", "no-cache"}
	if len(got.Charms) != len(want) {
		t.Fatalf("charms = %v, want %v", got.Charms, want)
	}
	for i, c := range want {
		if got.Charms[i] != c {
			t.Errorf("charm[%d] = %q, want %q", i, got.Charms[i], c)
		}
	}
}
