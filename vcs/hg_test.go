package vcs

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseHgConflicts pins the `hg resolve --list` parse. Only U is a conflict: an R
// line is a path already settled, and re-resolving one would clobber a resolution the
// user (or an earlier pass of this command) already made.
func TestParseHgConflicts(t *testing.T) {
	got := parseHgConflicts("U MAGUS.md\nR docs/index.md\nU gen/graph.json\n")
	require.Len(t, got, 2)
	assert.Equal(t, types.Conflict{Path: "MAGUS.md", Kind: types.ConflictKindContent}, got[0])
	assert.Equal(t, types.Conflict{Path: "gen/graph.json", Kind: types.ConflictKindContent}, got[1])

	t.Run("no merge in progress yields none", func(t *testing.T) {
		assert.Empty(t, parseHgConflicts(""))
	})
	t.Run("paths with spaces survive", func(t *testing.T) {
		got := parseHgConflicts("U docs/my notes.md\n")
		require.Len(t, got, 1)
		assert.Equal(t, "docs/my notes.md", got[0].Path)
	})
	t.Run("CRLF and malformed lines are skipped, not mis-parsed", func(t *testing.T) {
		got := parseHgConflicts("U a.md\r\ngarbage\nX b.md\n\n")
		require.Len(t, got, 1)
		assert.Equal(t, "a.md", got[0].Path)
	})
}

// TestParseHgRemovalCandidates pins the modify/delete detection against real
// `hg debugmergestate` output (mercurial 7.2.3).
//
// This guards a bug that shipped and was caught only by running hg: the first
// implementation probed `hg status -nd`, reasoning that a deleted file would show as
// missing. It does not. During a merge Mercurial keeps the local side in the working
// tree, so the file EXISTS and `status -nd` reports nothing; every modify/delete was
// silently classified as a content conflict, which regeneration cannot settle.
func TestParseHgRemovalCandidates(t *testing.T) {
	const out = `local (working copy): 1503fcb585e5
other (merge rev): 13f4d1a8ece2
file: f.txt (state "u")
  local path: f.txt (hash 7ad4af83b511, flags "")
  other path: f.txt (node 56086f771253)
  extra: merged = yes
file: gone.txt (state "u")
  local path: gone.txt (hash 0ce54e727589, flags "")
  other path: gone.txt (node 0000000000000000000000000000000000000000)
  extra: merge-removal-candidate = yes
  extra: merged = yes
`
	got := parseHgRemovalCandidates(out)
	assert.True(t, got["gone.txt"], "the side with no content is a removal candidate")
	assert.False(t, got["f.txt"], "a two-sided content conflict is not a removal")

	t.Run("unparseable output degrades to no deletions", func(t *testing.T) {
		assert.Empty(t, parseHgRemovalCandidates("debugmergestate: unknown command"))
	})
}

// TestBaseNamesTheMainlineNotTip is the regression test for hg's default base ref.
//
// tip is the newest commit in the repository, so once you have committed anything it is
// YOUR commit: ChangedFiles then compared the checkout against itself, `magus affected`
// reported nothing affected, and a working branch built nothing at all. Measured on
// Mercurial 7.x: on a named branch `--rev tip` returns empty where `--rev default` names
// the changed file.
//
// The assertion runs through ChangedFiles rather than reading Base() as a string, because
// the string alone cannot show the failure: "tip" looks perfectly reasonable until you diff
// against it from a branch.
func TestBaseNamesTheMainlineNotTip(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{"a.txt": "one\n"})

	vcsTestRun(t, dir, "hg", "branch", "feature")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("changed\n"), 0o644))
	vcsTestRun(t, dir, "hg", "commit", "-m", "feature work", "-u", "test")

	got, err := hgVCS{}.ChangedFiles(t.Context(), dir, hgVCS{}.Base())
	require.NoError(t, err, "ChangedFiles against the default base")
	assert.Contains(t, got, "a.txt",
		"a committed branch change reported nothing affected; affected would build nothing")
}

// A magus-shaped NAME is not proof magus wrote it: `hg shelve` with no --name derives the
// shelf name from the active bookmark, so a bookmark called magus-1234567890-wip yields a
// name shelfMinted accepts. Plain `hg shelve` also REVERTS the working copy, so that shelf
// can be the only copy of the work, and PrunePreserved runs unasked inside every Preserve.
func TestHgPrunePreservedSparesAShelfMagusDidNotWrite(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{"a.txt": "one\n"})

	// A user shelf shaped exactly like a mint; only the message tells the two apart.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("half a refactor\n"), 0o644))
	vcsTestRun(t, dir, "hg", "--config", "extensions.shelve=", "shelve",
		"--keep", "--name", "magus-1234567890-wip", "--message", "half of a refactor")

	// A real capture too, so the test cannot pass by pruning nothing at all.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("two\n"), 0o644))
	handle, err := hgVCS{}.Preserve(t.Context(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, handle)

	dropped, err := hgVCS{}.PrunePreserved(t.Context(), dir, time.Now().Add(time.Hour))
	require.NoError(t, err)

	assert.Equal(t, []string{handle}, dropped, "pruning reported something other than its own capture")
	names := hgListPreserved(t, dir)
	assert.Contains(t, names, "magus-1234567890-wip", "pruning deleted a shelf the user wrote")
	assert.NotContains(t, names, handle, "pruning reported a handle it did not delete")
}

// Cutoffs an hour either side of now leave preserveRetention itself untested: the constant
// could be thirty SECONDS and every assertion stays green. So this runs through Preserve
// rather than passing PrunePreserved a cutoff of its own, and asserts both sides of the
// window, since only the survival half pins the length.
//
// The two ages are LITERAL days, not preserveRetention plus or minus a day: written against
// the constant they move with it, and 29 either side of thirty seconds is still one on each
// side. Measured by setting the constant to thirty seconds.
func TestHgPreserveRetentionBoundaryIsThirtyDays(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{"a.txt": "one\n"})

	// The timestamp shelfName writes into the name is the only thing dating an hg shelf.
	aged := func(days int, suffix string) string {
		age := time.Duration(days) * 24 * time.Hour
		name := fmt.Sprintf("%s%d-%s", shelfPrefix, time.Now().Add(-age).Unix(), suffix)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte(name+"\n"), 0o644))
		vcsTestRun(t, dir, "hg", "--config", "extensions.shelve=", "shelve",
			"--keep", "--name", name, "--message", preserveMessage)
		return name
	}
	inside := aged(29, "inside")
	outside := aged(31, "outside")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("current\n"), 0o644))
	_, err := hgVCS{}.Preserve(t.Context(), dir)
	require.NoError(t, err)

	names := hgListPreserved(t, dir)
	assert.Contains(t, names, inside, "dropped a capture still inside the retention window")
	assert.NotContains(t, names, outside, "kept a capture past the retention window")
}

// A leading space is legal in a filename, and trimming it off a status line fails
// silently: restoring a deleted " leading.txt" runs `hg revert --no-backup -- leading.txt`,
// which prints "no such file in rev" and exits ZERO (Mercurial 7.2.3, 2026-09-09), so
// Preserve returns a handle and a nil error while the file stays scheduled for a removal
// the user never asked for. The assertion is on hg's own status rather than on the parse,
// because the parse looked right.
//
// A newline is checked one layer down: hg refuses to track a name carrying one (`add` and
// `addremove` both abort with "'\n' and '\r' disallowed in filenames"), so it can reach the
// unknown class and never the missing one.
func TestHgPreserveRestoresAPathCarryingWhitespace(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{" leading.txt": "l\n", "tracked.txt": "one\n"})

	require.NoError(t, os.Remove(filepath.Join(dir, " leading.txt")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("two\n"), 0o644))

	handle, err := hgVCS{}.Preserve(t.Context(), dir)
	require.NoError(t, err)
	require.NotEmpty(t, handle)

	status := vcsTestOutput(t, dir, "hg", "status")
	assert.NotContains(t, status, "R  leading.txt",
		"preserving left a deleted file scheduled for removal; the working copy is not what the user had")
	assert.Contains(t, status, "!  leading.txt",
		"the file the user deleted should still read as missing, not as restored")

	t.Run("an unknown path carrying a newline stays one path", func(t *testing.T) {
		weird := "we\nird.txt"
		require.NoError(t, os.WriteFile(filepath.Join(dir, weird), []byte("x\n"), 0o644))
		got, err := hgStatusPaths(t.Context(), "hg", dir, "--unknown")
		require.NoError(t, err)
		assert.Equal(t, []string{weird}, got, "a newline in a name split one path into two")
	})
}

// hgHistoryRepo builds assertHistoryScenario's history with prog, hg or sl, whose
// commands for it are the same.
func hgHistoryRepo(t *testing.T, prog string) string {
	t.Helper()
	if _, err := exec.LookPath(prog); err != nil {
		t.Skipf("%s not available", prog)
	}
	dir := t.TempDir()
	run := func(args ...string) { vcsTestRun(t, dir, prog, args...) }
	if prog == "sl" {
		slInitRepo(t, dir, map[string]string{"docs/a.md": "a\n", "x.txt": "x\n"})
	} else {
		hgInitRepo(t, dir, map[string]string{"docs/a.md": "a\n", "x.txt": "x\n"})
	}
	commit := func(msg string) { run("commit", "-m", msg, "-u", "test") }
	writeRepoFile(t, dir, "docs/b.md", "b\n")
	run("add", "docs/b.md")
	commit("side adds docs")
	run("update", "-r", "desc('init')")
	writeRepoFile(t, dir, "docs/a.md", "a2\n")
	commit("main edits docs")
	run("merge", "-r", "desc('side adds docs')")
	commit("merge side")
	writeRepoFile(t, dir, "x.txt", "x2\n")
	commit("main edits x")
	return dir
}

func TestHgHistoryFollowsPathsAndFirstParent(t *testing.T) {
	assertHistoryScenario(t, hgVCS{}, hgHistoryRepo(t, "hg"))
}

// A count that disagrees with the fields after it is an error, never a guess at where the
// next commit starts.
func TestParseHgHistoryRefusesABadCount(t *testing.T) {
	rec := strings.Join([]string{"id", "s", "n", "e", "2026-01-02T03:04:05Z", "", "msg"}, commitDelim)
	got, err := parseHgHistory(rec + commitDelim + "2" + commitDelim + "a" + commitDelim)
	assert.Nil(t, got)
	require.Error(t, err)

	got, err = parseHgHistory(rec + commitDelim + "1" + commitDelim + "a" + commitDelim)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, []string{"a"}, got[0].Files)
}

func TestDirstateCheckoutIDReadsBothFormats(t *testing.T) {
	dir := t.TempDir()
	dot := filepath.Join(dir, ".hg")
	require.NoError(t, os.MkdirAll(dot, 0o755))
	p1 := bytes.Repeat([]byte{0xab}, 20)
	p2 := bytes.Repeat([]byte{0xcd}, 20)
	require.NoError(t, os.WriteFile(filepath.Join(dot, "dirstate"), append(append([]byte{}, p1...), p2...), 0o644))
	id, ok := dirstateCheckoutID(dot)
	require.True(t, ok)
	assert.Equal(t, hex.EncodeToString(p1)+hex.EncodeToString(p2), id)

	var v2 []byte
	v2 = append(v2, []byte("dirstate-v2\n")...)
	v2 = append(v2, p1...)
	v2 = append(v2, bytes.Repeat([]byte{0}, 12)...)
	v2 = append(v2, p2...)
	v2 = append(v2, bytes.Repeat([]byte{0}, 12)...)
	require.NoError(t, os.WriteFile(filepath.Join(dot, "dirstate"), v2, 0o644))
	id, ok = dirstateCheckoutID(dot)
	require.True(t, ok)
	assert.Equal(t, hex.EncodeToString(append(p1, bytes.Repeat([]byte{0}, 12)...))+hex.EncodeToString(append(p2, bytes.Repeat([]byte{0}, 12)...)), id)

	require.NoError(t, os.WriteFile(filepath.Join(dot, "dirstate"), []byte("short"), 0o644))
	_, ok = dirstateCheckoutID(dot)
	assert.False(t, ok)
}

func TestHgCheckoutIDIsTheParentAndStatusLeavesIt(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{"a.buzz": "one\n"})
	node, err := vcsOutput(t.Context(), dir, "hg", "log", "-r", ".", "-T", "{node}")
	require.NoError(t, err)
	id, ok := hgVCS{}.CheckoutID(dir)
	require.True(t, ok)
	assert.Equal(t, node+strings.Repeat("0", 40), id)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.buzz"), []byte("two\n"), 0o644))
	vcsTestRun(t, dir, "hg", "status")
	again, ok := hgVCS{}.CheckoutID(dir)
	require.True(t, ok)
	assert.Equal(t, id, again, "status rewrites the dirstate stat cache and must leave the parent")

	vcsTestRun(t, dir, "hg", "commit", "-m", "edit", "-u", "test")
	committed, ok := hgVCS{}.CheckoutID(dir)
	require.True(t, ok)
	assert.NotEqual(t, id, committed)
}

func TestHgObjectBatchReadsCommittedFiles(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{"a.buzz": "one\n", "dir/b.buzz": "two\n"})
	batch, err := hgVCS{}.OpenObjectBatch(t.Context(), dir, "")
	require.NoError(t, err)
	defer func() { require.NoError(t, batch.Close()) }()

	for _, rel := range []string{"a.buzz", "dir/b.buzz"} {
		want, err := hgVCS{}.ReadFileAt(t.Context(), dir, ".", rel)
		require.NoError(t, err)
		got, err := batch.Read(rel)
		require.NoError(t, err)
		assert.Equal(t, want, got, rel)
	}
	_, err = batch.Read("no/such.buzz")
	require.ErrorIs(t, err, ErrObjectMissing)
	got, err := batch.Read("a.buzz")
	require.NoError(t, err)
	assert.Equal(t, "one\n", got)
}

// cmdFrames encodes command-server messages: a channel byte, a big-endian
// length, then the data.
func cmdFrames(msgs ...string) string {
	var buf bytes.Buffer
	for _, m := range msgs {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(m)-1))
		buf.WriteByte(m[0])
		buf.Write(n[:])
		buf.WriteString(m[1:])
	}
	return buf.String()
}

func fakeCmdBatch(stdout string) *cmdBatch {
	return &cmdBatch{
		prog:   "hg",
		rev:    ".",
		stdin:  nopWriteCloser{&bytes.Buffer{}},
		stdout: bufio.NewReader(strings.NewReader(stdout)),
		stderr: &bytes.Buffer{},
	}
}

// The command-server protocol makes an unknown lowercase channel optional, so
// a server that adds a debug channel still answers.
func TestCmdBatchSkipsAnUnknownLowercaseChannel(t *testing.T) {
	b := fakeCmdBatch(cmdFrames("ddebug", "oone\n", "r\x00\x00\x00\x00"))
	got, err := b.Read("a.buzz")
	require.NoError(t, err)
	assert.Equal(t, "one\n", got)

	b = fakeCmdBatch(cmdFrames("I\x00\x00\x10\x00"))
	_, err = b.Read("a.buzz")
	require.ErrorContains(t, err, "channel 'I'")
}

// A malformed reply leaves the stream out of step with its commands, so every
// later read returns that failure instead of a reply meant for another path.
func TestCmdBatchFailureIsSticky(t *testing.T) {
	b := fakeCmdBatch(cmdFrames("r\x00\x00", "otwo\n", "r\x00\x00\x00\x00"))
	_, first := b.Read("a.buzz")
	require.Error(t, first)
	_, err := b.Read("b.buzz")
	assert.Equal(t, first, err)
}

func TestKeepUnder(t *testing.T) {
	files := []string{"docs/a.md", "docsx/b.md", "README.md", "blog/p.md"}
	assert.Equal(t, files, keepUnder(files, nil))
	assert.Equal(t, []string{"docs/a.md", "README.md"}, keepUnder(files, []string{"docs/", "README.md"}))
	assert.Nil(t, keepUnder(files, []string{"nope"}))
}
