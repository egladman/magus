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
// output via ctx.writesFiles (the shape settleTarget requires), and which would leave
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

// writeResultFile writes git's %A file: the current version, and the file the VCS reads
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
// output the project declares, dirtying the working tree against what git had staged, so
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
	// is gitignored, and ignored files do NOT block `git rebase --continue`; it is the
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

	base, _, theirs := mergeSides(t, "x\n", "", "x2\n")
	err := mergeDriverRun(ctx, root, []string{base, result, theirs, "7", "notes.txt"})
	require.Error(t, err, "a code path both sides edited must not be auto-resolved")
	assert.ErrorContains(t, err, "not auto-resolved: notes.txt: not settled")
}

// TestMergeDriverRefusesUnrebuildableOutput covers the case that makes auto-resolution safe
// in the first place. A project-wide output glob with no target that writes it names no
// command a human could run afterwards, so keeping one side would drop the other's change
// with no conflict marker, and the VCS only invokes a driver when BOTH sides changed the
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

// autoResolveWorkspace keeps the built-in markdown prose globs, opts fixtures/** code in
// through merge_low_risk, and declares gen/** an output its generate target rebuilds.
func autoResolveWorkspace(t *testing.T) (context.Context, string) {
	t.Helper()
	root := t.TempDir()
	magusfile := `import "magus";

magus.project({"merge_low_risk": ["fixtures/**"]})

export fun generate(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles("gen/**");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
	m, err := magus.Open(context.Background(), root)
	require.NoError(t, err)
	return withMagus(context.Background(), m), root
}

// mergeSides writes a driver's three input files and returns their paths.
func mergeSides(t *testing.T, base, ours, theirs string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	paths := [3]string{filepath.Join(dir, "base"), filepath.Join(dir, "ours"), filepath.Join(dir, "theirs")}
	for i, body := range []string{base, ours, theirs} {
		require.NoError(t, os.WriteFile(paths[i], []byte(body), 0o644))
	}
	return paths[0], paths[1], paths[2]
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// A low-risk file settles through merge3 in git's shape, where the result replaces %A,
// and in jj's, where it goes to the separate output and the sides stay as they were.
// Prose qualifies by the change classifier's default globs, code only by merge_low_risk.
func TestMergeDriverAutoResolvesALowRiskFile(t *testing.T) {
	ctx, root := autoResolveWorkspace(t)

	base, ours, theirs := mergeSides(t, "a\nz\n", "a\np\nz\n", "a\nq\nz\n")
	require.NoError(t, mergeDriverRun(ctx, root, []string{base, ours, theirs, "7", "CHANGELOG.md"}))
	assert.Equal(t, "a\np\nq\nz\n", readFile(t, ours), "both additions, ours first")

	base, ours, theirs = mergeSides(t, "a\nz\n", "a\np\nz\n", "a\nq\nz\n")
	output := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.WriteFile(output, []byte("jj's markers\n"), 0o644))
	require.NoError(t, mergeDriverRun(ctx, root, []string{base, ours, theirs, "7", "CHANGELOG.md", output}))
	assert.Equal(t, "a\np\nq\nz\n", readFile(t, output))
	assert.Equal(t, "a\np\nz\n", readFile(t, ours), "jj's left side is an input")

	base, ours, theirs = mergeSides(t, "a\nz\n", "a\np\nz\n", "a\nq\nz\n")
	require.NoError(t, mergeDriverRun(ctx, root, []string{base, ours, theirs, "7", "fixtures/a.txt"}))
	assert.Equal(t, "a\np\nq\nz\n", readFile(t, ours), "code a project opts in")
}

// What does not settle is left conflicted with markers, and the error names the region's
// location or the classifier's verdict: git keeps %A exactly as a failed driver leaves
// it, so without markers the other side's change would be lost. jj's output already
// holds jj's markers and is not touched.
func TestMergeDriverLeavesAnUnsettledFileConflicted(t *testing.T) {
	ctx, root := autoResolveWorkspace(t)

	base, ours, theirs := mergeSides(t, "# T\nx\nz\n", "# T\nx1\nz\n", "# T\nx2\nz\n")
	err := mergeDriverRun(ctx, root, []string{base, ours, theirs, "7", "CHANGELOG.md"})
	require.ErrorContains(t, err, "not auto-resolved: CHANGELOG.md## T: not settled", "the region is the heading it sits under")
	assert.Equal(t, "# T\n<<<<<<< ours\nx1\n||||||| base\nx\n=======\nx2\n>>>>>>> theirs\nz\n", readFile(t, ours))

	base, ours, theirs = mergeSides(t, "a\nz\n", "a\np\nz\n", "a\nq\nz\n")
	err = mergeDriverRun(ctx, root, []string{base, ours, theirs, "7", "main.txt"})
	require.ErrorContains(t, err, "main.txt: code (no comment syntax is declared for this language; classified as code); main.txt: kind 2",
		"merge3 settles it, and the classifier refuses code nothing opts in")
	assert.Contains(t, readFile(t, ours), "a\np\nq\nz\n", "the markers of a merge that settled are the merge")

	base, ours, theirs = mergeSides(t, "", "p\n", "q\n")
	err = mergeDriverRun(ctx, root, []string{base, ours, theirs, "7", "CHANGELOG.md"})
	require.ErrorContains(t, err, "the merge base has no content for it", "git's empty ancestor for a file both sides added")

	base, ours, theirs = mergeSides(t, "a\nx\nz\n", "a\nx1\nz\n", "a\nx2\nz\n")
	output := filepath.Join(t.TempDir(), "output")
	require.NoError(t, os.WriteFile(output, []byte("jj's markers\n"), 0o644))
	require.Error(t, mergeDriverRun(ctx, root, []string{base, ours, theirs, "7", "CHANGELOG.md", output}))
	assert.Equal(t, "jj's markers\n", readFile(t, output))
}

// A declared output is regenerated, never auto-resolved: the driver keeps the current
// version whatever merge3 would make of it.
func TestMergeDriverKeepsAnOutputWhatever(t *testing.T) {
	ctx, root := autoResolveWorkspace(t)
	base, ours, theirs := mergeSides(t, "a\nz\n", "a\np\nz\n", "a\nq\nz\n")
	require.NoError(t, mergeDriverRun(ctx, root, []string{base, ours, theirs, "7", "gen/catalog.md"}))
	assert.Equal(t, "a\np\nz\n", readFile(t, ours))
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
