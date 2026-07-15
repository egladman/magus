package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
)

// TestInspect_TargetPolicyNamingUnknownTarget exercises the A4 fix end to end,
// through a real magusfile evaluated by the linked Buzz interpreter (cmd/magus
// blank-imports internal/interp/bindings via packs_interp.go, unlike the bare
// library's own test suite - see TestDescribeTargets_CustomTargets in the root
// package). A per-target policy naming a target the project declares nowhere
// (neither a custom export fun nor a spell op) must fail Inspect with an
// actionable error instead of silently producing a phantom target.
func TestInspect_TargetPolicyNamingUnknownTarget(t *testing.T) {
	root := t.TempDir()
	magusfile := `export fun build(args: [str]) > void {}

magus.project({
    "targets": {
        "bogus-target": {"skipCache": true}
    }
})
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))

	_, err := magus.Inspect(context.Background(), root)
	require.Error(t, err)
	assert.ErrorContains(t, err, `per-target policy names unknown target "bogus-target"`)
	assert.ErrorContains(t, err, "declared targets: build")
}

// TestInspect_TargetPolicyNamingKnownTargetOK is the control: a policy naming a
// target the magusfile actually declares must load cleanly.
func TestInspect_TargetPolicyNamingKnownTargetOK(t *testing.T) {
	root := t.TempDir()
	magusfile := `export fun build(args: [str]) > void {}

magus.project({
    "targets": {
        "build": {"skipCache": true}
    }
})
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))

	ws, err := magus.Inspect(context.Background(), root)
	require.NoError(t, err)
	p := ws.Get(".")
	require.NotNil(t, p)
	assert.True(t, p.TargetPolicies["build"].SkipCache)
}
