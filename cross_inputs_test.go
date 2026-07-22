package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCrossFileInputs is the end-to-end payoff of the unified per-target inputs: a target
// declaring ctx.inputs(<sibling>.file("go.mod"), "app/**") stores BOTH a cross-project
// and a same-project input in ONE TargetInputs list, each carrying its owning project.
// The cross input folds into the cache key WORKSPACE-relative (lib/go.mod, NOT
// consumer/lib/go.mod) and unions its owning project into DependsOn; the same-project
// input folds relative to the consumer (consumer/app/**) and adds NO self-dependency. A
// change to either file marks the consumer affected - the cross one via the DependsOn
// reverse-closure, the same-project one via directory containment. All recovered
// statically at Open, no runtime dispatch.
func TestCrossFileInputs(t *testing.T) {
	root := t.TempDir()

	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}

	// Workspace root marker.
	write("magusfile.buzz", "")
	// The owning project and the file the consumer reaches for.
	write("lib/magusfile.buzz", "export fun compile(ctx: magus\\Context, args: [str]) > void {}\n")
	write("lib/go.mod", "module lib\n")
	// The consumer declares a cross-project AND a same-project input on the same target.
	write("consumer/app/main.go", "package app\n")
	write("consumer/magusfile.buzz", `import "project/../lib" as lib;
export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.inputs(lib.file("go.mod"), "app/**");
}
`)

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	consumer := m.Get("consumer")
	require.NotNil(t, consumer, "consumer project must be discovered")

	// ONE list holds both inputs, each with its resolved owning project: the same-project
	// literal owned by "consumer" (folded relative to it), the cross input owned by "lib"
	// (folded workspace-relative, never re-anchored onto the consumer's path).
	assert.Equal(t, []types.InputRef{
		{Project: "consumer", Glob: "app/**"},
		{Project: "lib", Glob: "go.mod"},
	}, consumer.TargetInputs["build"],
		"same-project and cross-project inputs share one TargetInputs list, each owning-project-tagged")

	// The cross input's owning project is unioned into DependsOn - required for
	// affected-tracking (a DependsOn-reverse-closure). The same-project input adds no
	// self-edge (the depgraph rejects self-loops; it seeds by directory containment).
	assert.Contains(t, consumer.DependsOn, "lib",
		"the cross-input owning project must be a project-level dependency of the consumer")
	assert.NotContains(t, consumer.DependsOn, "consumer",
		"a same-project input must not add a self-dependency")

	// The buildStep hashes each input at its workspace-relative path.
	step := m.buildStep(consumer, "build")
	assert.Contains(t, step.Sources, "lib/go.mod",
		"the cross-input enters the cache key workspace-relative")
	assert.Contains(t, step.Sources, "consumer/app/**",
		"the same-project input enters the cache key relative to the consumer")

	// The payoff, both ways: editing lib/go.mod marks the consumer affected via DependsOn.
	res, err := m.AffectedFromPaths(context.Background(), []string{"lib/go.mod"})
	require.NoError(t, err, "AffectedFromPaths (cross)")
	assert.Contains(t, res.Affected, "consumer",
		"a change to a declared cross-input file must mark the consumer affected")

	// And editing the same-project input file marks the consumer affected via containment.
	res, err = m.AffectedFromPaths(context.Background(), []string{"consumer/app/main.go"})
	require.NoError(t, err, "AffectedFromPaths (same-project)")
	assert.Contains(t, res.Affected, "consumer",
		"a change to a same-project input file must mark the consumer affected")
}
