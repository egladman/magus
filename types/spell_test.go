package types

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// TestSpellVersionProbe covers the toolchain version-probe accessor: a spell
// with no probe reports false and returns ("", nil); a spell with a probe
// surfaces its result (and error) verbatim.
func TestSpellVersionProbe(t *testing.T) {
	none := NewSpell("none")
	if none.HasVersionProbe() {
		t.Error("HasVersionProbe = true for a spell with no probe")
	}
	if v, err := none.ProbeVersion(context.Background(), "/dir"); v != "" || err != nil {
		t.Errorf("ProbeVersion (no probe) = %q, %v; want \"\", nil", v, err)
	}

	probed := NewSpell("go", WithVersionProbe(func(_ context.Context, dir string) (string, error) {
		return "ver@" + dir, nil
	}))
	if !probed.HasVersionProbe() {
		t.Error("HasVersionProbe = false for a spell with a probe")
	}
	if v, err := probed.ProbeVersion(context.Background(), "/d"); v != "ver@/d" || err != nil {
		t.Errorf("ProbeVersion = %q, %v; want %q, nil", v, err, "ver@/d")
	}

	boom := errors.New("boom")
	failing := NewSpell("rs", WithVersionProbe(func(_ context.Context, _ string) (string, error) {
		return "", boom
	}))
	if _, err := failing.ProbeVersion(context.Background(), "/d"); !errors.Is(err, boom) {
		t.Errorf("ProbeVersion error = %v; want boom", err)
	}
}

func TestNewSpellAccessors(t *testing.T) {
	s := NewSpell(
		"go",
		WithSources("**/*.go"),
		WithClaims("**/*.go"),
		WithSpellOutputs("bin/**"),
		WithTargets("build", "test"),
		WithForeignProcess(),
	)
	if s.Name() != "go" {
		t.Errorf("Name() = %q, want go", s.Name())
	}
	if !slices.Equal(s.Targets(), []string{"build", "test"}) {
		t.Errorf("Targets() = %v", s.Targets())
	}
	if !s.ForeignProcess() {
		t.Error("ForeignProcess() = false, want true")
	}
}

// WithTargetSources must defensively clone its input so a caller mutating the
// original map after construction cannot corrupt the spell's internal state.
func TestWithTargetSourcesClonesInput(t *testing.T) {
	src := map[string][]string{"test": {"testdata/**"}}
	s := NewSpell("go", WithTargetSources(src))

	// Mutate the caller's map after construction.
	src["test"] = []string{"hacked"}
	src["lint"] = []string{"added"}

	if got := s.TargetSources()["test"]; !slices.Equal(got, []string{"testdata/**"}) {
		t.Errorf("TargetSources(test) = %v, want [testdata/**] (input not cloned)", got)
	}
	if got := s.TargetSources()["lint"]; got != nil {
		t.Errorf("TargetSources(lint) = %v, want nil (input not cloned)", got)
	}
}

func TestSpellNilInvokerIsNoop(t *testing.T) {
	// A spell with no invoke function is a graceful no-op, not a panic.
	s := NewSpell("noop")
	resp, err := s.Invoke(t.Context(), InvokeRequest{Target: "build"})
	if err != nil {
		t.Errorf("Invoke on nil-invoke spell: unexpected error %v", err)
	}
	if resp != (InvokeResponse{}) {
		t.Errorf("Invoke response = %+v, want zero", resp)
	}
}

func TestSpellImplementsSpellDriver(t *testing.T) {
	var _ SpellDriver = NewSpell("x")
}
