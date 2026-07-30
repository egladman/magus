package magus

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// openTempWorkspace creates a minimal workspace in a temp dir with a single
// project registered at projPath with the given output globs.
func openTempWorkspace(t *testing.T, projPath string, outputs []string) (*Magus, string) {
	t.Helper()
	root := t.TempDir()

	// An empty magusfile.buzz at the root marks it as the workspace root.
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	// An empty magusfile.buzz in the project directory makes magus discover it.
	projDir := filepath.Join(root, filepath.FromSlash(projPath))
	require.NoError(t, os.MkdirAll(projDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(projDir, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	if len(outputs) > 0 {
		reg.RegisterProject(projPath, WithOutputs(outputs...))
	}

	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	return m, root
}

// TestCleanOutputsRemovesMatchedFiles verifies that CleanOutputs deletes
// files matching declared Outputs globs and returns their paths.
func TestCleanOutputsRemovesMatchedFiles(t *testing.T) {
	m, root := openTempWorkspace(t, "api", []string{"bin/api", "gen/**"})

	binDir := filepath.Join(root, "api", "bin")
	genDir := filepath.Join(root, "api", "gen")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	require.NoError(t, os.MkdirAll(genDir, 0o755))

	// Create files that should be cleaned.
	binFile := filepath.Join(binDir, "api")
	genFile := filepath.Join(genDir, "types.pb.go")
	for _, f := range []string{binFile, genFile} {
		require.NoError(t, os.WriteFile(f, []byte("placeholder"), 0o644))
	}

	projects := m.All()
	removed, err := m.CleanOutputs(context.Background(), projects, false)
	require.NoError(t, err, "CleanOutputs")
	require.NotEmpty(t, removed, "CleanOutputs: no files removed")
	for _, path := range removed {
		_, err := os.Stat(path)
		assert.True(t, os.IsNotExist(err), "file still exists after clean: %s", path)
	}
}

// TestCleanOutputsCoversPerTargetOutputs verifies that a per-target
// magus.outputs declaration is cleaned by `magus clean --outputs`, not just
// project-wide outputs - AllOutputs unions the per-target globs back in.
func TestCleanOutputsCoversPerTargetOutputs(t *testing.T) {
	root := t.TempDir()
	const mf = `export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.outputs("gen/**");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	genFile := filepath.Join(root, "gen", "types.pb.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(genFile), 0o755))
	require.NoError(t, os.WriteFile(genFile, []byte("generated"), 0o644))

	removed, err := m.CleanOutputs(context.Background(), m.All(), false)
	require.NoError(t, err, "CleanOutputs")
	require.NotEmpty(t, removed, "a per-target magus.outputs file must be cleaned")
	// Assert on existence, not the exact path: t.TempDir resolves through /private
	// on macOS, so p.Dir and the test-built path differ by that prefix.
	_, statErr := os.Stat(genFile)
	assert.True(t, os.IsNotExist(statErr), "per-target output should be deleted")
}

// TestCleanOutputsDryRunDoesNotDelete verifies that --dry-run lists matched
// files without deleting them.
func TestCleanOutputsDryRunDoesNotDelete(t *testing.T) {
	m, root := openTempWorkspace(t, "api", []string{"bin/api"})

	binDir := filepath.Join(root, "api", "bin")
	require.NoError(t, os.MkdirAll(binDir, 0o755))
	target := filepath.Join(binDir, "api")
	require.NoError(t, os.WriteFile(target, []byte("binary"), 0o755))

	projects := m.All()
	removed, err := m.CleanOutputs(context.Background(), projects, true /* dryRun */)
	require.NoError(t, err, "CleanOutputs (dry-run)")
	require.NotEmpty(t, removed, "dry-run: expected at least one matched path to be returned")
	// File must still exist.
	_, err = os.Stat(target)
	assert.NoError(t, err, "dry-run: file was deleted")
}

// TestCleanOutputsNoMatchIsNoop verifies that CleanOutputs is a no-op when
// no output files exist on disk.
func TestCleanOutputsNoMatchIsNoop(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", []string{"bin/api"})
	projects := m.All()
	removed, err := m.CleanOutputs(context.Background(), projects, false)
	require.NoError(t, err, "CleanOutputs (no files)")
	assert.Empty(t, removed, "expected no removals")
}

// findProducer runs m.FindOutputProducer on a workspace-relative path and returns the
// project path it resolved to, or "" for no match. m.Root() is symlink-resolved by
// project.Discover, so query paths are built on it rather than the raw t.TempDir - the
// two differ by /private on macOS.
func findProducer(t *testing.T, m *Magus, relPath string) string {
	t.Helper()
	p := m.FindOutputProducer(filepath.Join(m.Root(), filepath.FromSlash(relPath)))
	if p == nil {
		return ""
	}
	return p.Path
}

// TestFindOutputProducerMatchesDeclaration verifies that FindOutputProducer returns the
// project for a path matching one of its own Output globs, and nothing for a path no
// project declares.
func TestFindOutputProducerMatchesDeclaration(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", []string{"bin/**", "gen/**"})

	assert.Equal(t, "api", findProducer(t, m, "api/bin/api"))
	assert.Equal(t, "api", findProducer(t, m, "api/gen/types.pb.go"))
	assert.Empty(t, findProducer(t, m, "api/src/main.go"), "not an output")
	assert.Empty(t, findProducer(t, m, "other/bin/app"), "different project")
}

// TestFindOutputProducerReturnsWriterNotOwner is the distinction the merge driver rests
// on: for a file one project writes into ANOTHER's tree, the producer is the WRITER.
// Returning the owner would have the driver run a target that touches nothing, then copy
// the unregenerated file over the conflict and report a clean merge.
func TestFindOutputProducerReturnsWriterNotOwner(t *testing.T) {
	m, _ := writeCrossOutputWorkspace(t)

	assert.Equal(t, "producer", findProducer(t, m, "site/generated.txt"),
		"only the writer can regenerate a file it writes into another tree")
}

// writeCrossOutputWorkspace builds the canonical cross-project-output workspace: a
// `producer` project whose build target declares ctx.outputs(site.file(...)) and writes
// into a SIBLING project's tree. It returns the opened workspace and the absolute path of
// the file producer generates inside site.
//
// The body never runs here - the interpreter is a pack only the CLI wires in, so these
// tests exercise the static half (the derived edge, the glob views, clean). The
// execution half is cmd/magus's TestCrossProjectOutputReplaysFromCache, which has it.
func writeCrossOutputWorkspace(t *testing.T) (*Magus, string) {
	t.Helper()
	root := t.TempDir()

	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}

	write("magusfile.buzz", "")
	write("site/magusfile.buzz", "")
	write("producer/magusfile.buzz", `import "magus";
import "fs";
import "project/../site" as site;

export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.outputs(site.file("generated.txt"));
    fs\writeFile("../site/generated.txt", "hello");
}
`)

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	// m.Root() is symlink-resolved by project.Discover; build the path on it so it
	// shares that canonical base (t.TempDir resolves through /private on macOS).
	return m, filepath.Join(m.Root(), "site", "generated.txt")
}

// TestCrossProjectOutputInvertsDependency asserts the edge a cross-project output
// implies runs OPPOSITE to a cross-project input's: producer WRITES into site, so site
// consumes producer and must build after it. Getting this backwards would not fail
// loudly - it would silently order the build wrong and hand site a stale tree.
func TestCrossProjectOutputInvertsDependency(t *testing.T) {
	m, _ := writeCrossOutputWorkspace(t)

	site := m.Get("site")
	require.NotNil(t, site, "site project must be discovered")
	assert.Contains(t, site.DependsOn, "producer",
		"the project WRITTEN INTO depends on the writer, not the other way round")

	producer := m.Get("producer")
	require.NotNil(t, producer, "producer project must be discovered")
	assert.NotContains(t, producer.DependsOn, "site",
		"declaring an output into site must not make producer depend on site")
}

// TestCleanOutputsCoversCrossProjectOutputs asserts `magus clean` reaches an output one
// project declares into ANOTHER project's tree. The writer cannot surface it - its own
// globs are relative to its own root, and this one is not - so it is filed on the OWNER
// at load, as InboundOutputs. Without that hand-off nothing would clean the file: the
// writer's view excludes it and the owner never declared it.
func TestCleanOutputsCoversCrossProjectOutputs(t *testing.T) {
	m, generated := writeCrossOutputWorkspace(t)

	require.NoError(t, os.WriteFile(generated, []byte("hello"), 0o644))

	removed, err := m.CleanOutputs(context.Background(), m.All(), false)
	require.NoError(t, err, "CleanOutputs")
	require.NotEmpty(t, removed, "a cross-project output must be cleaned")
	_, statErr := os.Stat(generated)
	assert.True(t, os.IsNotExist(statErr), "cross-project output should be deleted")
}

// TestInboundOutputsLandOnTheOwner pins the hand-off AllOutputs depends on: a
// cross-project glob is filed on the project it lands IN, keyed by the WRITER, and
// relative to the OWNER's root - which is why AllOutputs can union it without
// re-anchoring. The writer's own view must not claim it, or every caller that joins
// AllOutputs to a project dir would resolve it against the wrong root.
func TestInboundOutputsLandOnTheOwner(t *testing.T) {
	m, _ := writeCrossOutputWorkspace(t)

	site, producer := m.Get("site"), m.Get("producer")
	require.NotNil(t, site)
	require.NotNil(t, producer)

	assert.Equal(t, map[string][]string{"producer": {"generated.txt"}}, site.InboundOutputs,
		"the owner records the glob under the writer that produces it")
	assert.Equal(t, []string{"generated.txt"}, site.AllOutputs(),
		"the owner's complete view carries it, relative to ITS root")
	assert.Empty(t, producer.AllOutputs(),
		"the writer must not claim a glob that is relative to another project's root")
	assert.Empty(t, producer.InboundOutputs,
		"nothing is written into the writer's own tree")
}

// TestProjectsResolvesTargets verifies that Projects returns the correct
// project records for a given set of targets.
func TestProjectsResolvesTargets(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)

	targets := []types.Target{{Path: "api", Name: "build"}}
	projects := m.ResolveProjects(targets)
	require.Len(t, projects, 1, "Projects")
	assert.Equal(t, "api", projects[0].Path)
}

// TestOutputReadsAreConcurrencySafe hammers the exported read paths from many goroutines
// under -race. The cross-project index is built once during Open and never written after,
// so these must be pure reads; the test is what pins that, since the failure mode of a
// lazily-populated index would be a data race rather than a wrong answer.
func TestOutputReadsAreConcurrencySafe(t *testing.T) {
	m, generated := writeCrossOutputWorkspace(t)
	site := m.Get("site")
	require.NotNil(t, site)

	const goroutines = 16
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assert.Equal(t, []string{"generated.txt"}, site.AllOutputs())
			assert.Equal(t, "producer", m.FindOutputProducer(generated).Path)
			_, err := m.CleanOutputs(context.Background(), m.All(), true /* dryRun */)
			assert.NoError(t, err)
		}()
	}
	wg.Wait()
}

// TestAllOutputsNeverAliasesProjectOutputs guards the exported result against a caller
// that appends to it. p.Outputs carries spare capacity from AttachSpell, so returning the
// live backing array would let one append reach into the project record - and the hazard
// would be invisible in any workspace whose content happened to take a copying branch.
func TestAllOutputsNeverAliasesProjectOutputs(t *testing.T) {
	p := &types.Project{Path: "api", Outputs: make([]string, 1, 8)}
	p.Outputs[0] = "dist/**"

	got := p.AllOutputs()
	_ = append(got, "scribbled")

	assert.Equal(t, []string{"dist/**"}, p.Outputs, "appending to the result must not reach the project")
}

// TestCrossOutputMutualRefIsRejectedAtLoad covers the shape this feature most invites:
// a target that reads a project's sources AND writes its generated file back. The two
// cross edges run opposite ways, so declaring both against one project closes a 2-cycle.
//
// It has to fail at LOAD. Left to the depgraph it surfaced as a bare "graph: dependency
// cycle" naming neither project nor file, it took down `magus graph` and the whole
// affected pipeline rather than the two projects involved, and it was scope-dependent -
// a run selecting only the writer never built the full graph, so `magus run build`
// reported success on a workspace `magus graph deps` refused to load.
func TestCrossOutputMutualRefIsRejectedAtLoad(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}
	write("magusfile.buzz", "")
	write("site/magusfile.buzz", "")
	write("site/src.md", "source")
	write("renderer/magusfile.buzz", `import "magus";
import "project/../site" as site;

export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.inputs(site.file("src.md"));
    ctx.outputs(site.file("out.html"));
}
`)

	_, err := Open(context.Background(), root)
	require.Error(t, err, "a target reading from and writing into one project must not load")
	assert.ErrorIs(t, err, types.CrossOutputCycle)
	assert.Contains(t, err.Error(), "out.html", "the error names the output that closes the cycle")
	assert.Contains(t, err.Error(), "site", "the error names the other project")
}

// writeWorkspace lays down a workspace from rel-path -> content and returns the root.
func writeWorkspace(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}
	return root
}

// crossWriter is a magusfile whose build target writes glob into the sibling project
// named by dir.
func crossWriter(dir, glob string) string {
	return `import "magus";
import "project/../` + dir + `" as owner;

export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.outputs(owner.file("` + glob + `"));
}
`
}

// TestTwoWritersClaimingOnePathAreRejected is the cache-poisoning guard. Verified before
// the check existed: both writers ran concurrently, last write won, and BOTH snapshotted
// the file - so the loser's cache entry held the winner's bytes, and replaying the loser
// alone reproduced content it had never produced.
//
// The pre-existing MGS4002 advisory cannot cover this. It is run-scoped, so it needs both
// writers in one dispatch; it keeps only the first stage's step per project; and it lets
// the run proceed regardless. This fires at load, so no run scope can hide it.
func TestTwoWritersClaimingOnePathAreRejected(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":      "",
		"site/magusfile.buzz": "",
		"p1/magusfile.buzz":   crossWriter("site", "shared.txt"),
		"p2/magusfile.buzz":   crossWriter("site", "shared.txt"),
	})

	_, err := Open(context.Background(), root)
	require.Error(t, err, "two projects declaring one output path must not load")
	assert.Contains(t, err.Error(), "shared.txt")
	assert.Contains(t, err.Error(), "p1")
	assert.Contains(t, err.Error(), "p2", "the error names both claimants, not just the one walked last")
}

// TestWriterClaimingOwnersOwnOutputIsRejected covers the other collision shape: the owner
// already declares the path for itself, so its own build produces and cleans a file the
// writer also owns.
func TestWriterClaimingOwnersOwnOutputIsRejected(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz": "",
		"site/magusfile.buzz": `export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.outputs("shared.txt");
}
`,
		"p1/magusfile.buzz": crossWriter("site", "shared.txt"),
	})

	_, err := Open(context.Background(), root)
	require.Error(t, err, "a writer must not claim a path the owner already declares")
	assert.Contains(t, err.Error(), "shared.txt")
	assert.Contains(t, err.Error(), "site")
}

// TestDistinctPathsIntoOneTreeAreAllowed is the negative: two writers into ONE tree is
// the point of the feature, and only an exact path collision is an error. Both edges
// land, so the owner waits on both.
func TestDistinctPathsIntoOneTreeAreAllowed(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":      "",
		"site/magusfile.buzz": "",
		"p1/magusfile.buzz":   crossWriter("site", "from-p1.txt"),
		"p2/magusfile.buzz":   crossWriter("site", "from-p2.txt"),
	})

	m, err := Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	site := m.Get("site")
	require.NotNil(t, site)
	assert.Equal(t, map[string][]string{"p1": {"from-p1.txt"}, "p2": {"from-p2.txt"}}, site.InboundOutputs)
	assert.Equal(t, []string{"p1", "p2"}, site.DependsOn, "the owner waits on every writer")
}

// TestCrossOutputGlobEscapingOwnerIsRejected pins the glob half of a cross-project
// output to the owner's subtree. Before this, the pattern was accepted, the target RAN,
// and the failure surfaced only at snapshot - or, worse, in `magus clean`, which expands
// every project's globs in one loop and so aborted the whole workspace over one project's
// bad declaration.
func TestCrossOutputGlobEscapingOwnerIsRejected(t *testing.T) {
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz":      "",
		"site/magusfile.buzz": "",
		"p1/magusfile.buzz":   crossWriter("site", "../../etc/x"),
	})

	_, err := Open(context.Background(), root)
	require.Error(t, err)
	assert.ErrorIs(t, err, types.CrossOutputGlobEscapes, "the error carries a researchable code")
	assert.Contains(t, err.Error(), "must not contain ..")
}

// TestCrossOutputDiagnosticsCarryCodes guards the property that makes these errors
// actionable rather than dead ends: each names an MGS code the reader can look up. They
// are authoring mistakes found at load, which is exactly when a pointer to an explanation
// is worth most.
func TestCrossOutputDiagnosticsCarryCodes(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		code  types.DiagnosticCode
	}{
		{
			name: "owner is not a discovered project",
			files: map[string]string{
				"magusfile.buzz":     "",
				"gen/magusfile.buzz": "", // gen/ is an ignore-dir: importable, never discovered
				"p1/magusfile.buzz":  crossWriter("gen", "x.txt"),
			},
			code: types.CrossOutputOwnerUnknown,
		},
		{
			name: "target reads from and writes into one project",
			files: map[string]string{
				"magusfile.buzz":      "",
				"site/magusfile.buzz": "",
				"site/src.md":         "source",
				"p1/magusfile.buzz": `import "magus";
import "project/../site" as site;

export fun build(ctx: magus\Context, args: [str]) > void {
    ctx.inputs(site.file("src.md"));
    ctx.outputs(site.file("out.html"));
}
`,
			},
			code: types.CrossOutputCycle,
		},
		{
			name: "two projects claim one path",
			files: map[string]string{
				"magusfile.buzz":      "",
				"site/magusfile.buzz": "",
				"p1/magusfile.buzz":   crossWriter("site", "shared.txt"),
				"p2/magusfile.buzz":   crossWriter("site", "shared.txt"),
			},
			code: types.OutputOverlapDetected,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Open(context.Background(), writeWorkspace(t, tc.files))
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.code)
			assert.Contains(t, types.CodeURL(tc.code), "/reference/codes/",
				"the code resolves to a docs page the reader can reach")
		})
	}
}

// TestCleanSkipsUpdates is the regression for the incident that motivated
// ctx.updates: `magus clean docs` deleted docs/concepts/spells.md, 355 lines of
// hand-written prose carrying a 13-line generated table between markers, because the
// whole file was declared in ctx.outputs. A file magus only EDITS is not magus's to
// delete - regeneration rewrites the marked region, not the prose around it.
func TestCleanSkipsUpdates(t *testing.T) {
	root := t.TempDir()
	const mf = `export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.outputs("gen/**");
    ctx.updates("concepts/spells.md");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	produced := filepath.Join(root, "gen", "types.pb.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(produced), 0o755))
	require.NoError(t, os.WriteFile(produced, []byte("generated"), 0o644))

	edited := filepath.Join(root, "concepts", "spells.md")
	const prose = "# Spells\n\nAuthored prose.\n\n<!-- BEGIN SPELL LIST -->\n<!-- END SPELL LIST -->\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(edited), 0o755))
	require.NoError(t, os.WriteFile(edited, []byte(prose), 0o644))

	_, err = m.CleanOutputs(context.Background(), m.All(), false)
	require.NoError(t, err, "CleanOutputs")

	_, statErr := os.Stat(produced)
	assert.True(t, os.IsNotExist(statErr), "a declared output should still be deleted")

	got, readErr := os.ReadFile(edited)
	require.NoError(t, readErr, "ctx.updates file must survive clean")
	assert.Equal(t, prose, string(got), "ctx.updates file must survive clean byte for byte")
}

// TestUpdatesFoldIntoSourcesNotOutputs pins the asymmetry that makes ctx.updates
// worth having over ctx.outputs: the file lands in the cache key (so editing the
// authored prose invalidates the target that maintains the generated region) and
// stays out of the output set (so it is never snapshotted, replayed, or cleaned).
func TestUpdatesFoldIntoSourcesNotOutputs(t *testing.T) {
	root := t.TempDir()
	const mf = `export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.updates("concepts/spells.md");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(mf), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	p := m.All()[0]
	require.Equal(t, []types.UpdateRef{{Project: ".", Glob: "concepts/spells.md"}},
		p.TargetUpdates["generate"], "ctx.updates should resolve to the declaring project")
	assert.NotContains(t, p.AllOutputs(), "concepts/spells.md",
		"an update must never reach AllOutputs, which is what clean and the snapshot walk")
}
