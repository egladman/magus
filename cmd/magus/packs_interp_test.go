package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/libs/testkit"
)

// These tests live beside packs_interp.go because that file is what makes them possible: the
// blank imports there link the Buzz interpreter into this binary, so Inspect here evaluates a
// real magusfile. The bare library's own suite cannot; see TestListTargets_CustomTargets in
// the root package, which asserts the same rule with no interpreter behind it.

// TestInspect_TargetPolicyNamingUnknownTarget exercises the A4 fix end to end,
// through a real magusfile evaluated by the linked Buzz interpreter (cmd/magus
// blank-imports internal/interp/bindings via packs_interp.go, unlike the bare
// library's own test suite; see TestListTargets_CustomTargets in the root
// package). A per-target policy naming a target the project declares nowhere
// (neither a custom export fun nor a spell op) must fail Inspect with an
// actionable error instead of silently producing a phantom target.
func TestInspect_TargetPolicyNamingUnknownTarget(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Magusfile(`import "magus";

export fun build(ctx: magus\Context, args: [str]) > void {}

magus\project({
    "targets": {
        "bogus-target": {"skip_cache": "test policy"}
    }
})
`)

	_, err := magus.Inspect(context.Background(), w.Root(), magus.WithSpawnRule(magus.TierCeiling))
	require.Error(t, err)
	assert.ErrorContains(t, err, `per-target policy names unknown target "bogus-target"`)
	assert.ErrorContains(t, err, "declared targets: build")
}

// TestInspect_TargetPolicyNamingKnownTargetOK is the control: a policy naming a
// target the magusfile actually declares must load cleanly.
func TestInspect_TargetPolicyNamingKnownTargetOK(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Magusfile(`import "magus";

export fun build(ctx: magus\Context, args: [str]) > void {}

magus\project({
    "targets": {
        "build": {"skip_cache": "test policy"}
    }
})
`)

	ws, err := magus.Inspect(context.Background(), w.Root(), magus.WithSpawnRule(magus.TierCeiling))
	require.NoError(t, err)
	p := ws.Get(".")
	require.NotNil(t, p)
	assert.True(t, p.TargetPolicies["build"].SkipCache)
}

// TestInspect_ReportsEveryFailingMagusfile pins that one load names every broken
// magusfile, and that the policy check stays quiet about a registry the failed
// magusfiles never populated.
func TestInspect_ReportsEveryFailingMagusfile(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root, write := w.Root(), w.Write
	write("magusfile.buzz", `import "magus";

magus\project({
    "targets": {
        "bogus-target": {"skip_cache": "test policy"}
    }
})
`)
	write("a/magusfile.buzz", "magus.project({});\n")
	write("b/magusfile.buzz", "magus.project({});\n")

	_, err := magus.Inspect(context.Background(), root, magus.WithSpawnRule(magus.TierCeiling))
	require.Error(t, err)
	assert.ErrorContains(t, err, "magus: a: ")
	assert.ErrorContains(t, err, "magus: b: ")
	assert.NotContains(t, err.Error(), "per-target policy")
}
