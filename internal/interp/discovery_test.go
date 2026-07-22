package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// writeCtxMagusfile writes content as a magusfile.buzz in a fresh temp dir and
// returns a buzz Source pointing at it.
func writeCtxMagusfile(t *testing.T, content string) *Source {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	return &Source{Dir: dir, Files: []string{path}, Engine: "buzz"}
}

// nodesByName indexes discovered nodes by their (normalized) name.
func nodesByName(nodes []types.TargetGraphNode) map[string]types.TargetGraphNode {
	m := map[string]types.TargetGraphNode{}
	for _, n := range nodes {
		m[n.Name] = n
	}
	return m
}

// TestDiscoverCtxNodes runs a magusfile of ctx-form targets under discovery and
// asserts the recorded declarations (needs, glob, inputs, outputs, doc) become
// graph nodes, that has_charm records the charm and takes its false branch, and that
// an exported non-ctx function is ignored (not a target under the new contract).
func TestDiscoverCtxNodes(t *testing.T) {
	src := writeCtxMagusfile(t, `
import "magus";

// Format the code.
export fun format(ctx: magus\Context, args: [str]) > void {}

// Build the binary from Go sources.
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.needs(format);
    ctx.inputs("cmd/**/*.go");
    ctx.outputs("bin/app");
    ctx.skip_cache();
    ctx.slots(2);
}

// Lint by pattern.
export fun lint(ctx: magus\Context, args: [str]) > void {
    ctx.needs(ctx.glob("forma*"));
}

// Conditionally emit a release artifact.
export fun release(ctx: magus\Context, args: [str]) > void {
    if (ctx.has_charm("cd")) {
        ctx.outputs("dist/pkg.tar.gz");
    }
}

// A plain exported helper - NOT a target (no ctx first param).
export fun helper(args: [str]) > void {}
`)

	nodes, policies, err := DiscoverCtxNodes(context.Background(), src)
	require.NoError(t, err)

	by := nodesByName(nodes)
	assert.NotContains(t, by, "helper", "an exported non-ctx function must not be a ctx-form target")
	require.Len(t, nodes, 4, "expected build, format, lint, release")

	assert.Equal(t, types.TargetGraphNode{
		Name:         "build",
		Doc:          "Build the binary from Go sources.",
		Dependencies: []string{"format"},
		Inputs:       []types.InputRef{{Glob: "cmd/**/*.go"}},
		Outputs:      []string{"bin/app"},
	}, by["build"])

	// glob resolves the pattern to a handle that needs records as a dependency.
	assert.Equal(t, []string{"format"}, by["lint"].Dependencies, "needs(glob(forma*)) matches format")

	// has_charm returns false under discovery (the charm-absent branch), so the
	// guarded output is not recorded, but the charm name IS recorded on the node.
	assert.Equal(t, []string{"cd"}, by["release"].Charms)
	assert.Empty(t, by["release"].Outputs, "the has_charm(cd) branch is not taken under discovery")

	// ctx.skip_cache()/ctx.slots(2) on build become a TargetPolicies entry; format,
	// which declared no policy, gets none.
	assert.Equal(t, types.Target{SkipCache: true, Slots: 2}, policies["build"])
	_, hasFormat := policies["format"]
	assert.False(t, hasFormat, "a target that declared no policy gets no entry")
}

// TestDiscoverCtxNodesNoSideEffects proves discovery does not execute the body's
// effectful host ops: a target that writes a file records its declarations but the
// file is never created (discovery is a superset of dry-run tracing, so fs.writeFile
// no-ops).
func TestDiscoverCtxNodesNoSideEffects(t *testing.T) {
	src := writeCtxMagusfile(t, `
import "magus";
import "fs";

// Generate a file - must not actually write during discovery.
export fun gen(ctx: magus\Context, args: [str]) > void {
    ctx.outputs("out.txt");
    fs.writeFile("out.txt", "hello");
}
`)

	nodes, _, err := DiscoverCtxNodes(context.Background(), src)
	require.NoError(t, err)
	require.Len(t, nodes, 1)
	assert.Equal(t, []string{"out.txt"}, nodes[0].Outputs)

	_, statErr := os.Stat(filepath.Join(src.Dir, "out.txt"))
	assert.True(t, os.IsNotExist(statErr), "fs.writeFile must not run during discovery")
}

// TestDiscoverCtxNodesOldFormOnly returns nil for a magusfile with no ctx-form
// targets (the old global-magus form stays with describe.Extract).
func TestDiscoverCtxNodesOldFormOnly(t *testing.T) {
	src := writeCtxMagusfile(t, `
import "magus";

export fun build(args: [str]) > void { magus.needs(format); }
export fun format(args: [str]) > void {}
`)

	nodes, _, err := DiscoverCtxNodes(context.Background(), src)
	require.NoError(t, err)
	assert.Nil(t, nodes, "a magusfile with no ctx-form targets discovers no nodes")
}

// TestCtxFormTargetKeys asserts the name-only signature check the ci guard uses:
// an exported function whose first param is magus\Context is a ctx-form target key,
// and a non-ctx export is not.
func TestCtxFormTargetKeys(t *testing.T) {
	keys := CtxFormTargetKeys(`
import "magus";
export fun ci(ctx: magus\Context, args: [str]) > void {}
export fun build(args: [str]) > void {}
`)
	assert.True(t, keys["ci"], "ctx-form ci is a target key")
	assert.False(t, keys["build"], "an old-form export is not a ctx-form key")
}
