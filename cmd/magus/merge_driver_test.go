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

// mergeDriverWorkspace builds a workspace whose single project declares gen/** as an output
// and whose targets, if either ever ran, would leave gen/regenerated.txt behind. Both
// generate and build write it because pickRegenTarget falls back to build when no bound
// spell contributes a generate target, as this fixture's does not - with only generate
// declared the sentinel was unreachable and silently proved nothing.
func mergeDriverWorkspace(t *testing.T) (context.Context, string) {
	t.Helper()
	root := t.TempDir()
	magusfile := `import "magus";
import "fs";

magus.project({
    "outputs": ["gen/**"],
})

export fun generate(ctx: magus\Context, args: [str]) > void {
    fs\writeFile("gen/regenerated.txt", "the merge driver regenerated");
}

export fun build(ctx: magus\Context, args: [str]) > void {
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
	// The whole-tree comparison is the load-bearing assertion: running any target also
	// leaves cache manifests and output records behind, and it is that broader dirtying -
	// not just the sentinel - that blocks `git rebase --continue`.
	assert.Equal(t, before, snapshotTree(t, root),
		"the driver must leave the working tree untouched; a dirty tree is what blocks `git rebase --continue`")
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
