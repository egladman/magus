package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/types"
)

// TestCtxFormTargetFootprint is the end-to-end payoff of merging discovery into the
// cache-footprint path: a ctx-form target (first param magus\Context) that declares
// ctx.inputs/ctx.outputs has those recorded by running it under discovery at Open,
// landing in the SAME Project.TargetInputs/TargetOutputs the static extractor fills
// for the old global-magus form - so the target's cache key and snapshot set are
// correct and a change to a declared input marks the project affected.
//
// It lives in cmd/magus, which links the Buzz interpreter (packs_interp.go blank-imports
// internal/interp/bindings): discovery RUNS the target body, so the bare library test
// binary - which deliberately does not link the interpreter - cannot exercise it (see
// TestDescribeTargets_CustomTargets in the root package).
func TestCtxFormTargetFootprint(t *testing.T) {
	root := t.TempDir()

	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}

	write("magusfile.buzz", "")
	write("app/src/main.go", "package app\n")
	write("app/magusfile.buzz", `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.inputs("src/**/*.go");
    ctx.outputs("bin/app");
}
`)

	m, err := magus.Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	app := m.Get("app")
	require.NotNil(t, app, "app project must be discovered")

	// The ctx-recorded same-project input lands in TargetInputs, owned by this project
	// (folded relative to it), exactly as an old-form magus.inputs("src/**/*.go") would.
	assert.Equal(t, []types.InputRef{{Project: "app", Glob: "src/**/*.go"}}, app.TargetInputs["build"],
		"ctx.inputs must populate the same TargetInputs the static extractor fills")
	assert.Equal(t, []string{"bin/app"}, app.TargetOutputs["build"],
		"ctx.outputs must populate TargetOutputs")

	// The payoff: editing a declared-input file marks the project affected.
	res, err := m.AffectedFromPaths(context.Background(), []string{"app/src/main.go"})
	require.NoError(t, err, "AffectedFromPaths")
	assert.Contains(t, res.Affected, "app",
		"a change to a ctx-declared input file must mark the project affected")
}
