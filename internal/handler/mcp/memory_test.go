package mcp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// journalEntries builds a synthetic append-journal of n dated entries, matching the
// "## <heading>\n\n<body>\n\n" shape the append op stamps, so the parser/windower
// and the rotate compaction are exercised against real entry framing.
func journalEntries(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "## 2026-01-%02d - entry %d\n\nbody of entry %d\n\n", i, i, i)
	}
	return b.String()
}

func TestParseMemoryEntries(t *testing.T) {
	preamble, entries := parseMemoryEntries(journalEntries(3))
	assert.Empty(t, preamble, "a pure journal has no preamble")
	require.Len(t, entries, 3)
	assert.Equal(t, "2026-01-01 - entry 1", entries[0].heading)
	assert.Equal(t, "2026-01-03 - entry 3", entries[2].heading, "entries stay in file order")
	assert.Contains(t, entries[1].text, "## 2026-01-02 - entry 2")
	assert.Contains(t, entries[1].text, "body of entry 2")

	// Content before the first heading is returned as preamble, not swallowed.
	pre, ents := parseMemoryEntries("intro line\n\n" + journalEntries(1))
	assert.Equal(t, "intro line", pre)
	require.Len(t, ents, 1)

	// A file with no headings is all preamble, no entries.
	pre2, ents2 := parseMemoryEntries("no headings here\n")
	assert.Equal(t, "no headings here", pre2)
	assert.Empty(t, ents2)

	// Only a DATE-stamped "## " line is an entry boundary. A plain "## " subheading
	// pasted into a note body stays part of that one entry rather than splitting it.
	withSub := "## 2026-01-02 - title\n\nbody line\n\n## Not A Date\n\nstill the same entry\n"
	pre3, ents3 := parseMemoryEntries(withSub)
	assert.Empty(t, pre3)
	require.Len(t, ents3, 1, "a non-dated ## subheading must not start a new entry")
	assert.Contains(t, ents3[0].text, "## Not A Date")
	assert.Contains(t, ents3[0].text, "still the same entry")

	// A leading non-dated "## " line is preamble, not an entry.
	pre4, ents4 := parseMemoryEntries("## Intro\n\nsome text\n")
	assert.Equal(t, "## Intro\n\nsome text", pre4)
	assert.Empty(t, ents4)
}

func TestWindowMemoryReturnsTOCPlusLastN(t *testing.T) {
	// At or under the window, the content is returned verbatim - nothing to collapse.
	small := journalEntries(memoryReadWindow)
	assert.Equal(t, small, windowMemory(small, memoryReadWindow))

	// Over the window: a TOC of every heading plus only the last N entry bodies.
	out := windowMemory(journalEntries(9), memoryReadWindow)
	assert.Contains(t, out, "Table of contents")
	for i := 1; i <= 9; i++ {
		assert.Contains(t, out, fmt.Sprintf("entry %d", i), "every heading appears in the TOC")
	}
	// The last memoryReadWindow=5 bodies (entries 5..9) are present in full; the
	// earlier bodies (1..4) are collapsed to their TOC line only.
	assert.Contains(t, out, "body of entry 9")
	assert.Contains(t, out, "body of entry 5")
	assert.NotContains(t, out, "body of entry 4")
	assert.Contains(t, out, "op=read_all", "the window tells the reader how to get the full journal")
}

func TestRotateProgressDirKeepsWindowArchivesRest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "progress.md"), []byte(journalEntries(12)), 0o644))
	// A decisions log sits beside it and must be left completely untouched.
	decisions := journalEntries(4)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "decisions.md"), []byte(decisions), 0o644))

	kept, archived, err := rotateProgressDir(dir, 5)
	require.NoError(t, err)
	assert.Equal(t, 5, kept)
	assert.Equal(t, 7, archived)

	// Live journal now holds only the recent window (entries 8..12).
	live, err := os.ReadFile(filepath.Join(dir, "progress.md"))
	require.NoError(t, err)
	_, liveEntries := parseMemoryEntries(string(live))
	require.Len(t, liveEntries, 5)
	assert.Equal(t, "2026-01-08 - entry 8", liveEntries[0].heading)
	assert.Equal(t, "2026-01-12 - entry 12", liveEntries[4].heading)

	// Archive holds the older remainder (entries 1..7), in order.
	arch, err := os.ReadFile(filepath.Join(dir, "progress.archive.md"))
	require.NoError(t, err)
	_, archEntries := parseMemoryEntries(string(arch))
	require.Len(t, archEntries, 7)
	assert.Equal(t, "2026-01-01 - entry 1", archEntries[0].heading)
	assert.Equal(t, "2026-01-07 - entry 7", archEntries[6].heading)

	// Decisions is byte-for-byte unchanged: it is never auto-pruned.
	gotDecisions, err := os.ReadFile(filepath.Join(dir, "decisions.md"))
	require.NoError(t, err)
	assert.Equal(t, decisions, string(gotDecisions))
}

func TestRotateProgressDirNoopWithinWindow(t *testing.T) {
	dir := t.TempDir()
	content := journalEntries(3)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "progress.md"), []byte(content), 0o644))

	kept, archived, err := rotateProgressDir(dir, 5)
	require.NoError(t, err)
	assert.Equal(t, 3, kept)
	assert.Equal(t, 0, archived)

	got, err := os.ReadFile(filepath.Join(dir, "progress.md"))
	require.NoError(t, err)
	assert.Equal(t, content, string(got), "a journal within the window is left as-is")
	_, err = os.Stat(filepath.Join(dir, "progress.archive.md"))
	assert.True(t, os.IsNotExist(err), "no archive is created when nothing is compacted")
}

func TestRotateProgressDirMissingJournal(t *testing.T) {
	kept, archived, err := rotateProgressDir(t.TempDir(), 5)
	require.NoError(t, err, "a missing journal is not an error")
	assert.Zero(t, kept)
	assert.Zero(t, archived)
}

// TestRepoIdentityWorktree proves every worktree of one repo resolves to the
// same identity, so they share one memory directory - the reason the key is
// not the checkout path.
func TestRepoIdentityWorktree(t *testing.T) {
	main := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git"), 0o755))

	wt := t.TempDir()
	gitfile := "gitdir: " + filepath.Join(main, ".git", "worktrees", "feature-x") + "\n"
	require.NoError(t, os.WriteFile(filepath.Join(wt, ".git"), []byte(gitfile), 0o644))

	assert.Equal(t, main, repoIdentity(main), "a plain checkout identifies as itself")
	assert.Equal(t, main, repoIdentity(wt), "a linked worktree identifies as the main repo")

	// No .git at all (other VCS, bare dir): the root is the identity.
	other := t.TempDir()
	assert.Equal(t, other, repoIdentity(other))
}

func TestMemoryDirIsOutsideRepoAndStable(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := t.TempDir()

	dir, err := memoryDir(root)
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(dir))
	assert.Contains(t, dir, filepath.Join(state, "magus", "memory"))
	assert.NotContains(t, dir, root, "memory must not live under the repo")
	assert.Contains(t, filepath.Base(dir), filepath.Base(root)+"-", "dir name leads with the repo basename for human legibility")

	again, err := memoryDir(root)
	require.NoError(t, err)
	assert.Equal(t, dir, again, "the key is deterministic")
}

func TestMemoryAppendIsDateStamped(t *testing.T) {
	dir := t.TempDir()
	_, err := scratchpadOpFile(dir, "progress.md", "append", "## 2026-01-02\n\nshipped the thing\n")
	require.NoError(t, err)
	// The Invoke path stamps before calling scratchpadOpFile; this test covers
	// the file semantics the stamp rides on: appends accumulate, reads return all.
	res, err := scratchpadOpFile(dir, "progress.md", "append", "## 2026-01-03\n\nfixed the other thing\n")
	require.NoError(t, err)
	assert.Contains(t, res.Content, "2026-01-02")
	assert.Contains(t, res.Content, "2026-01-03")

	read, err := scratchpadOpFile(dir, "progress.md", "read", "")
	require.NoError(t, err)
	assert.Equal(t, res.Content, read.Content)
}
