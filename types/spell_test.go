package types_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/egladman/magus/types"
)

// TestSpellVersionProbe covers the toolchain version-probe accessor: a spell
// with no probe reports false and returns ("", nil); a spell with a probe
// surfaces its result (and error) verbatim.
func TestSpellVersionProbe(t *testing.T) {
	none := types.NewSpell("none")
	if none.HasVersionProbe() {
		t.Error("HasVersionProbe = true for a spell with no probe")
	}
	if v, err := none.ProbeVersion(context.Background(), "/dir"); v != "" || err != nil {
		t.Errorf("ProbeVersion (no probe) = %q, %v; want \"\", nil", v, err)
	}

	probed := types.NewSpell("go", types.WithVersionProbe(func(_ context.Context, dir string) (string, error) {
		return "ver@" + dir, nil
	}))
	if !probed.HasVersionProbe() {
		t.Error("HasVersionProbe = false for a spell with a probe")
	}
	if v, err := probed.ProbeVersion(context.Background(), "/d"); v != "ver@/d" || err != nil {
		t.Errorf("ProbeVersion = %q, %v; want %q, nil", v, err, "ver@/d")
	}

	boom := errors.New("boom")
	failing := types.NewSpell("rs", types.WithVersionProbe(func(_ context.Context, _ string) (string, error) {
		return "", boom
	}))
	if _, err := failing.ProbeVersion(context.Background(), "/d"); !errors.Is(err, boom) {
		t.Errorf("ProbeVersion error = %v; want boom", err)
	}
}

func TestNewSpellAccessors(t *testing.T) {
	s := types.NewSpell(
		"go",
		types.WithSources("**/*.go"),
		types.WithClaims("**/*.go"),
		types.WithSpellOutputs("bin/**"),
		types.WithTargets("build", "test"),
		types.WithForeignProcess(),
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
	s := types.NewSpell("go", types.WithTargetSources(src))

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
	s := types.NewSpell("noop")
	resp, err := s.Invoke(t.Context(), types.InvokeRequest{Target: "build"})
	if err != nil {
		t.Errorf("Invoke on nil-invoke spell: unexpected error %v", err)
	}
	if resp != (types.InvokeResponse{}) {
		t.Errorf("Invoke response = %+v, want zero", resp)
	}
}

func TestSpellImplementsSpellDriver(t *testing.T) {
	var _ types.SpellDriver = types.NewSpell("x")
}
