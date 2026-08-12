package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mergeDriverWorkspace builds a workspace whose generate target declares gen/** as its own
// output via ctx.writesFiles - the shape settleTarget requires - and which would leave
// gen/regenerated.txt behind if that target ever ran.
func mergeDriverWorkspace(t *testing.T) (context.Context, string) {
	t.Helper()
	root := t.TempDir()
	magusfile := `import "magus";
import "fs";

magus.project({})

export fun generate(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles("gen/**");
    fs\writeFile("gen/regenerated.txt", "the merge driver regenerated");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gen"), 0o755))

	m, err := magus.Open(context.Background(), root)
	require.NoError(t, err, "fixture workspace must open")
	return withMagus(context.Background(), m), root
}

// writeResultFile writes git's %A file - the current version, and the file the VCS reads
// back as the merge result.
func writeResultFile(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, "result")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

// TestMergeDriverKeepsCurrentVersion pins the resolution: %A survives byte-for-byte and the
// driver reports success, so the VCS records the conflict as resolved with no hand-merged
// hunks and no conflict markers.
func TestMergeDriverKeepsCurrentVersion(t *testing.T) {
	ctx, root := mergeDriverWorkspace(t)

	const current = "generated: the current version\n"
	result := writeResultFile(t, t.TempDir(), current)

	err := mergeDriverRun(ctx, root, []string{"ancestor", result, "other", "7", "gen/catalog.md"})
	require.NoError(t, err, "a declared output must resolve cleanly")

	got, rerr := os.ReadFile(result)
	require.NoError(t, rerr)
	assert.Equal(t, current, string(got), "%A must be left exactly as the VCS staged it")
}

// TestMergeDriverDoesNotRegenerate is the regression test for the rebase loop. The driver
// used to run the owning project's generate target with write access, which rewrote every
// output the project declares - dirtying the working tree against what git had staged, so
// `git rebase --continue` refused to proceed. Nothing in the workspace may change here.
func TestMergeDriverDoesNotRegenerate(t *testing.T) {
	ctx, root := mergeDriverWorkspace(t)

	before := snapshotTree(t, root)
	result := writeResultFile(t, t.TempDir(), "generated: the current version\n")

	require.NoError(t, mergeDriverRun(ctx, root, []string{"ancestor", result, "other", "7", "gen/catalog.md"}))

	assert.NoFileExists(t, filepath.Join(root, "gen", "regenerated.txt"),
		"the driver must not run the project's regeneration target")
	// The whole-tree comparison is a broader "ran nothing" oracle than the sentinel alone,
	// catching cache manifests and output records too. Note those live under .magus/, which
	// is gitignored, and ignored files do NOT block `git rebase --continue` - it is the
	// TRACKED declared outputs that do. Both matter here: this asserts the driver ran no
	// target at all.
	assert.Equal(t, before, snapshotTree(t, root),
		"the driver must run no target; rewriting tracked outputs is what blocks `git rebase --continue`")
}

// TestMergeDriverRejectsUndeclaredPath keeps magus out of files it has no regeneration story
// for: a non-zero exit is what makes the VCS fall back to ordinary conflict markers, rather
// than silently picking a side of a file a human is expected to merge.
func TestMergeDriverRejectsUndeclaredPath(t *testing.T) {
	ctx, root := mergeDriverWorkspace(t)
	result := writeResultFile(t, t.TempDir(), "hand written\n")

	err := mergeDriverRun(ctx, root, []string{"ancestor", result, "other", "7", "README.md"})
	require.Error(t, err, "a path no project declares as an output must not be auto-resolved")
	assert.ErrorContains(t, err, "no project declares")
}

// TestMergeDriverRefusesUnrebuildableOutput covers the case that makes auto-resolution safe
// in the first place. A project-wide output glob with no target that writes it names no
// command a human could run afterwards, so keeping one side would drop the other's change
// with no conflict marker - and the VCS only invokes a driver when BOTH sides changed the
// file. go.mod and the lockfiles in this repo are wired to the driver in exactly this shape.
func TestMergeDriverRefusesUnrebuildableOutput(t *testing.T) {
	root := t.TempDir()
	// gen/** is declared project-wide, and no target claims it via ctx.writesFiles.
	magusfile := `import "magus";

magus.project({
    "outputs": ["gen/**"],
})

export fun build(ctx: magus\Context, args: [str]) > void {}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
	m, err := magus.Open(context.Background(), root)
	require.NoError(t, err)
	ctx := withMagus(context.Background(), m)

	result := writeResultFile(t, t.TempDir(), "generated: the current version\n")
	err = mergeDriverRun(ctx, root, []string{"ancestor", result, "other", "7", "gen/catalog.md"})

	require.Error(t, err, "a declared output no target rebuilds must not be auto-resolved")
	assert.ErrorContains(t, err, "rebuilds")
}

// TestMergeDriverArgCount covers the protocol contract: git always passes five placeholders,
// so anything shorter is a human at a prompt and must not report "resolved".
func TestMergeDriverArgCount(t *testing.T) {
	for name, args := range map[string][]string{
		"none":  {},
		"one":   {"ancestor"},
		"four":  {"ancestor", "result", "other", "7"},
		"empty": nil,
	} {
		t.Run(name, func(t *testing.T) {
			err := mergeDriverRun(context.Background(), t.TempDir(), args)
			require.Error(t, err)
			assert.ErrorContains(t, err, "expected 5 arguments")
		})
	}
}

// TestMergeDriverRelPath covers both callers' path conventions: git passes a repo-relative
// path, hg an absolute one.
func TestMergeDriverRelPath(t *testing.T) {
	t.Run("git passes a repo-relative path through", func(t *testing.T) {
		got, err := mergeDriverRelPath(t.TempDir(), filepath.Join("gen", "catalog.md"))
		require.NoError(t, err)
		assert.Equal(t, "gen/catalog.md", got, "must be normalized to a slash path")
	})

	t.Run("hg passes an absolute path, made workspace-relative", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte("magus.project({})\n"), 0o644))

		got, err := mergeDriverRelPath(root, filepath.Join(root, "gen", "catalog.md"))
		require.NoError(t, err)
		assert.Equal(t, "gen/catalog.md", got)
	})
}

// snapshotTree records every file under root by relative path and contents, so a test can
// assert the whole working tree is unchanged rather than guessing which paths to check.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[filepath.ToSlash(rel)] = string(body)
		return nil
	}))
	return out
}
