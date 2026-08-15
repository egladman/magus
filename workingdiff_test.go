package magus

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatusLinePath(t *testing.T) {
	cases := map[string]string{
		"?? cmd/magus/review.go":  "cmd/magus/review.go", // git porcelain, untracked
		" M magus.go":             "magus.go",            // git porcelain, modified (leading space)
		"A  internal/new.go":      "internal/new.go",     // staged add
		"R  old.go -> new.go":     "new.go",              // a rename keeps the name on disk
		"? scratch.txt":           "scratch.txt",         // hg
		"bare/path/with/no/state": "bare/path/with/no/state",
		"":                        "",
		"   ":                     "",
	}
	for line, want := range cases {
		assert.Equal(t, want, statusLinePath(line), "line %q", line)
	}
}

// A brand-new file is the thing a reviewer most wants to see, and a tree-against-index diff
// misses it entirely. This is the regression test for that gap.
func TestWorkingDiffIncludesUntrackedFiles(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")

	require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("one\ntwo\n"), 0o644))
	run("add", "tracked.txt")
	run("commit", "-qm", "seed")

	// One modified tracked file and one brand-new untracked file.
	require.NoError(t, os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("one\nCHANGED\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "fresh.txt"), []byte("hello\nworld\n"), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	patch, err := m.WorkingDiff(context.Background(), nil)
	require.NoError(t, err)

	assert.Contains(t, patch, "tracked.txt", "the tracked change must still be there")
	assert.Contains(t, patch, "diff --git a/fresh.txt b/fresh.txt",
		"an untracked file must appear, or a review cannot show a new file at all")
	assert.Contains(t, patch, "new file mode")
	assert.Contains(t, patch, "+hello")
	assert.Contains(t, patch, "+world")
}

func TestWorkingDiffMarksUntrackedBinaryRatherThanDumpingIt(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(root, "seed.txt"), []byte("x\n"), 0o644))
	run("add", "seed.txt")
	run("commit", "-qm", "seed")

	require.NoError(t, os.WriteFile(filepath.Join(root, "blob.bin"), []byte{0x00, 0x01, 0x02, 0x00}, 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	patch, err := m.WorkingDiff(context.Background(), nil)
	require.NoError(t, err)
	assert.Contains(t, patch, "Binary files /dev/null and b/blob.bin differ",
		"a binary file must be marked, not rendered as a wall of mojibake")
}

// The two halves are CONCATENATED, and a tracked diff whose last line is not
// newline-terminated would otherwise glue the first synthesized header onto it - so that
// header stops starting a line, every reader misses it, and the first untracked file vanishes
// from the review while the rest appear normally. It cost one real file here and reported
// nothing, which is what makes it worth a test rather than a comment.
func TestWorkingDiffSeparatesTheTrackedAndUntrackedHalves(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")

	// Seed a file with NO trailing newline, then change it: git renders "\ No newline at end
	// of file" and the diff's own last line is unterminated.
	require.NoError(t, os.WriteFile(filepath.Join(root, "nonewline.txt"), []byte("one"), 0o644))
	run("add", "nonewline.txt")
	run("commit", "-qm", "seed")
	require.NoError(t, os.WriteFile(filepath.Join(root, "nonewline.txt"), []byte("two"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "brand-new.txt"), []byte("fresh\n"), 0o644))

	m, err := Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	patch, err := m.WorkingDiff(context.Background(), nil)
	require.NoError(t, err)

	// The header must START a line. Asserting Contains would pass even when glued.
	var headers []string
	for _, l := range strings.Split(patch, "\n") {
		if strings.HasPrefix(l, "diff --git ") {
			headers = append(headers, l)
		}
	}
	assert.Contains(t, headers, "diff --git a/brand-new.txt b/brand-new.txt",
		"the first untracked file's header must begin a line, not be appended to the tracked half")
}

// A clean tree has nothing to review, and must not synthesize a patch out of ignored files.
func TestWorkingDiffOnACleanTreeIsEmpty(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
	}
	run("init", "-q")
	run("config", "user.email", "t@example.com")
	run("config", "user.name", "t")
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.txt"), []byte("x\n"), 0o644))
	run("add", "a.txt")
	run("commit", "-qm", "seed")

	m, err := Open(context.Background(), root)
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	patch, err := m.WorkingDiff(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, strings.TrimSpace(patch))
}
