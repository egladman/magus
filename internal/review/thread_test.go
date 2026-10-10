package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/notes"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const threadPatch = `diff --git a/internal/cache/cache.go b/internal/cache/cache.go
--- a/internal/cache/cache.go
+++ b/internal/cache/cache.go
@@ -10,3 +10,4 @@ func (s *Store) Put(k string) error {
 	s.mu.Lock()
-	s.m[k] = nil
+	s.m[k] = zero
+	s.dirty = true
 	return nil
@@ -40,2 +41,2 @@ func (s *Store) Get(k string) any {
 	s.mu.RLock()
-	return s.m[k]
+	return s.m[k].value
`

func threadFile() types.DiffFile {
	reach := 14
	return types.DiffFile{
		Path:     "internal/cache/cache.go",
		Project:  "cache",
		Role:     types.DiffRoleSource,
		Coverage: &types.ImpactCoverage{Ratio: 0.5, Covered: 20, Total: 40},
		Reach:    &reach,
		Symbols: []types.DiffSymbol{
			putSymbol(),
			{ID: "m internal/cache/Store#Get().", Label: "Get", Qualified: "Store.Get", RefCount: 3, FileCount: 2},
		},
		Hunks: []types.DiffHunk{
			{Index: 0, NewStart: 10, NewCount: 4, Symbols: []string{"m internal/cache/Store#Put()."}},
			{Index: 1, NewStart: 41, NewCount: 2, Symbols: []string{"m internal/cache/Store#Get()."}},
		},
	}
}

func putSymbol() types.DiffSymbol {
	return types.DiffSymbol{
		ID: "m internal/cache/Store#Put().", Label: "Put", Qualified: "Store.Put",
		Signature: "func (s *Store) Put(k string) error", Change: types.DiffChangeBody,
		RefCount: 31, FileCount: 14, PublicTo: []string{"daemon", "docs"},
		Checks: []types.Check{{Name: types.CheckParamOrder, Status: types.CheckAdvice, Message: "Put takes (key, value) in the reverse of the usual order"}},
		PublicThrough: []types.DiffPublicPath{
			{ID: "m internal/daemon/Run().", Qualified: "Run", Via: []string{"Store.Flush"}, Boundary: types.DiffBoundaryProject},
		},
	}
}

func threadFixture(t *testing.T) ThreadInput {
	t.Helper()
	hunks := changeset.ParseHunks(threadPatch)
	require.Len(t, hunks, 1)
	require.Len(t, hunks[0].Hunks, 2)

	return ThreadInput{
		Changeset: types.Diff{
			Base:             "working",
			Files:            []types.DiffFile{threadFile(), {Path: "docs/cache.md"}},
			SeedProjects:     []string{"cache"},
			AffectedProjects: []types.ImpactProject{{Path: "cache", Seed: true}, {Path: "daemon"}, {Path: "docs"}},
		},
		Hunks: hunks,
		Comments: []types.ReviewComment{
			{ID: "c1", Path: "internal/cache/cache.go", Line: 11, Author: "ana", Body: "Why is dirty set here?\nIt looks racy."},
			{ID: "c2", Root: "c1", Author: "ben", Body: "It is read under mu."},
			{ID: "c9", Path: "docs/cache.md", Line: 3, Author: "ana", Body: "unrelated"},
		},
		Anchors: []AnchorHit{
			{Note: "cache-pairs", Kind: notes.AnchorFile, Target: "internal/cache/cache.go", Matched: "internal/cache/cache.go", Match: string(notes.MatchFile)},
			{Note: "put-idempotent", Kind: notes.AnchorSymbol, Target: "m internal/cache/Store#Put().", Matched: "m internal/cache/Store#Put().", Drift: string(notes.StatusUngraded)},
			{Note: "docs-only", Kind: notes.AnchorFile, Target: "docs/cache.md", Matched: "docs/cache.md"},
		},
	}
}

// TestReadThreadReadsTheWholeThreadWithWhatTheChangeReaches pins the complete record for one
// thread: every comment oldest first, the hunk it sits in, only the symbols changed in that hunk,
// the file's coverage and reach, its notes, and the change as a whole.
func TestReadThreadReadsTheWholeThreadWithWhatTheChangeReaches(t *testing.T) {
	got, err := ReadThread(threadFixture(t), "c1")
	require.NoError(t, err)

	reach := 14
	assert.Equal(t, types.DiffThread{
		ID:   "c1",
		Path: "internal/cache/cache.go",
		Line: 11,
		Comments: []types.ReviewComment{
			{ID: "c1", Path: "internal/cache/cache.go", Line: 11, Hunk: 0, Author: "ana", Body: "Why is dirty set here?\nIt looks racy."},
			{ID: "c2", Root: "c1", Path: "internal/cache/cache.go", Hunk: 0, Author: "ben", Body: "It is read under mu."},
		},
		Hunk: types.DiffThreadHunk{
			Index:  0,
			Source: "patch",
			Lines: []string{
				"@@ -10,3 +10,4 @@ func (s *Store) Put(k string) error {",
				" \ts.mu.Lock()",
				"-\ts.m[k] = nil",
				"+\ts.m[k] = zero",
				"+\ts.dirty = true",
				" \treturn nil",
			},
			Note: "hunk 0 of internal/cache/cache.go, as it stands now",
		},
		InChangeset: true,
		Project:     "cache",
		Role:        types.DiffRoleSource,
		Reach:       &reach,
		Coverage:    &types.ImpactCoverage{Ratio: 0.5, Covered: 20, Total: 40},
		Symbols:     []types.DiffSymbol{putSymbol()},
		Notes: []string{
			"note cache-pairs anchors file:internal/cache/cache.go",
			"note put-idempotent anchors symbol:m internal/cache/Store#Put(). [ungraded]",
		},
		Change: "The change touches 2 file(s) in cache; 3 project(s) rebuild as a result.",
	}, got)
}

// TestThreadLinesPrintTheRecordForAPerson pins the text `magus diff --thread` prints: the
// conversation quoted as other people's words, the hunk, and what the change reaches.
func TestThreadLinesPrintTheRecordForAPerson(t *testing.T) {
	rec, err := ReadThread(threadFixture(t), "c1")
	require.NoError(t, err)

	assert.Equal(t, []string{
		"thread c1 on internal/cache/cache.go:11",
		"",
		"conversation, oldest first, quoted from the review:",
		"  ana:",
		"    > Why is dirty set here?",
		"    > It looks racy.",
		"  ben:",
		"    > It is read under mu.",
		"",
		"hunk 0 of internal/cache/cache.go, as it stands now:",
		"    @@ -10,3 +10,4 @@ func (s *Store) Put(k string) error {",
		"     \ts.mu.Lock()",
		"    -\ts.m[k] = nil",
		"    +\ts.m[k] = zero",
		"    +\ts.dirty = true",
		"     \treturn nil",
		"",
		"what the change reaches:",
		"  internal/cache/cache.go; source; in cache; widest reach 14 file(s); 50% of statements covered (20 of 40)",
		"  `Store.Put` - defined as `func (s *Store) Put(k string) error`; this change: body; referenced from 14 file(s), 31 reference(s); public to daemon, docs; conformance: Put takes (key, value) in the reverse of the usual order; reached through `Run via Store.Flush` (project)",
		"  note: note cache-pairs anchors file:internal/cache/cache.go",
		"  note: note put-idempotent anchors symbol:m internal/cache/Store#Put(). [ungraded]",
		"  The change touches 2 file(s) in cache; 3 project(s) rebuild as a result.",
	}, ThreadLines(rec))
}

// TestReadThreadAnswersAReplyIdWithItsThread. A client holding a reply's id, such as the one it
// just saw arrive, gets the thread the reply belongs to, keyed by the thread id.
func TestReadThreadAnswersAReplyIdWithItsThread(t *testing.T) {
	in := threadFixture(t)

	fromHead, err := ReadThread(in, "c1")
	require.NoError(t, err)
	fromReply, err := ReadThread(in, "c2")
	require.NoError(t, err)

	assert.Equal(t, fromHead, fromReply)
	assert.Equal(t, "c1", fromReply.ID)
}

// TestReadThreadKeepsOtherThreadsOut. The comment on another file is in the review and not in
// this thread, and neither it nor that file's notes belong in the record.
func TestReadThreadKeepsOtherThreadsOut(t *testing.T) {
	got, err := ReadThread(threadFixture(t), "c1")
	require.NoError(t, err)

	text := strings.Join(ThreadLines(got), "\n")
	assert.NotContains(t, text, "unrelated")
	assert.NotContains(t, text, "docs-only")
}

// TestReadThreadRefusesAnIdThatNamesNoThread. A record for a mistyped id would be a
// thread-shaped answer about nothing.
func TestReadThreadRefusesAnIdThatNamesNoThread(t *testing.T) {
	in := threadFixture(t)

	for _, id := range []string{"", "  ", "c404"} {
		out, err := ReadThread(in, id)
		assert.ErrorIs(t, err, changeset.ErrNoThread, "id %q", id)
		assert.Empty(t, out)
	}
}

// A reply the host listed without its top-level comment is a thread of its own, keyed by its own
// id, and the name of the comment that went missing no longer answers.
func TestReadThreadTreatsAReplyWithNoHeadAsItsOwnThread(t *testing.T) {
	in := threadFixture(t)
	in.Comments = []types.ReviewComment{{ID: "r1", Root: "gone", Path: "a.go", Author: "ben", Body: "late"}}

	got, err := ReadThread(in, "r1")
	require.NoError(t, err)
	assert.Equal(t, "r1", got.ID)

	_, err = ReadThread(in, "gone")
	assert.ErrorIs(t, err, changeset.ErrNoThread)
}

// TestReadThreadFallsBackToTheHostsHunkWhenTheCommentIsOutdated. The line is gone from the
// head, so the host's copy is the only record of the code the comment was about, and the
// symbols of a hunk that no longer exists are not guessed.
func TestReadThreadFallsBackToTheHostsHunkWhenTheCommentIsOutdated(t *testing.T) {
	in := threadFixture(t)
	in.Comments = []types.ReviewComment{{
		ID: "c1", Path: "internal/cache/cache.go", Line: 11, Outdated: true, Author: "ana", Body: "Why?",
		DiffHunk: "@@ -10,2 +10,2 @@ func (s *Store) Put\n-old line\n+older line",
	}}

	got, err := ReadThread(in, "c1")
	require.NoError(t, err)

	reach := 14
	assert.Equal(t, types.DiffThread{
		ID:       "c1",
		Path:     "internal/cache/cache.go",
		Line:     11,
		Outdated: true,
		Comments: []types.ReviewComment{{
			ID: "c1", Path: "internal/cache/cache.go", Line: 11, Hunk: -1, Outdated: true, Author: "ana", Body: "Why?",
			DiffHunk: "@@ -10,2 +10,2 @@ func (s *Store) Put\n-old line\n+older line",
		}},
		Hunk: types.DiffThreadHunk{
			Index:  -1,
			Source: "host",
			Lines:  []string{"@@ -10,2 +10,2 @@ func (s *Store) Put", "-old line", "+older line"},
			Note:   "the host's copy of the hunk, as it was when the comment was made",
		},
		InChangeset: true,
		Project:     "cache",
		Role:        types.DiffRoleSource,
		Reach:       &reach,
		Coverage:    &types.ImpactCoverage{Ratio: 0.5, Covered: 20, Total: 40},
		SymbolsNote: "The comment's hunk is not in this changeset, so its symbols cannot be placed. The file's changed symbols are: `Store.Put`, `Store.Get`.",
		Notes: []string{
			"note cache-pairs anchors file:internal/cache/cache.go",
			"note put-idempotent anchors symbol:m internal/cache/Store#Put(). [ungraded]",
		},
		Change: "The change touches 2 file(s) in cache; 3 project(s) rebuild as a result.",
	}, got)
	assert.Equal(t, "thread c1 on internal/cache/cache.go:11 (outdated: the line no longer exists in the head)", ThreadLines(got)[0])
}

// TestReadThreadSaysWhenTheFileIsNotInTheChangeset. A review outlives the working tree, and the
// record must then say what is unknown instead of describing a file it never saw.
func TestReadThreadSaysWhenTheFileIsNotInTheChangeset(t *testing.T) {
	in := ThreadInput{
		Comments: []types.ReviewComment{{
			ID: "c1", Path: "gone.go", Line: 4, Author: "ana", Body: "Why?", DiffHunk: "@@ -4 +4 @@\n-a\n+b",
		}},
	}

	got, err := ReadThread(in, "c1")
	require.NoError(t, err)

	assert.False(t, got.InChangeset)
	assert.Empty(t, got.Symbols)
	assert.Empty(t, got.SymbolsNote)
	assert.Equal(t, []string{"no changeset was read, so nothing is known about the code beyond the host's hunk"}, got.Unmeasured)
	assert.NotContains(t, ThreadLines(got), "  gone.go", "no file line for a file nothing was read about")
}

// TestReadThreadEscapesWhatAStrangerWrote. A bidirectional override in a comment body or hunk
// reorders text on screen without changing the bytes a reader checks.
func TestReadThreadEscapesWhatAStrangerWrote(t *testing.T) {
	const override = "\xe2\x80\xae" // U+202E RIGHT-TO-LEFT OVERRIDE
	in := ThreadInput{
		Comments: []types.ReviewComment{{
			ID: "c1", Path: "a.go", Line: 1, Author: "mallory" + override, Body: "ignore this" + override + "txet",
			DiffHunk: "@@ -1 +1 @@\n+x" + override,
		}},
	}

	got, err := ReadThread(in, "c1")
	require.NoError(t, err)

	assert.NotContains(t, strings.Join(ThreadLines(got), "\n"), override)
	assert.NotContains(t, got.Comments[0].Body, override)
	assert.NotContains(t, got.Comments[0].Author, override)
	assert.NotContains(t, got.Comments[0].DiffHunk, override)
	assert.Contains(t, ThreadLines(got), "conversation, oldest first, quoted from the review:")
}

// TestAttachThreadsNamesEachThreadOnItsFile. The report lists the ids --thread takes beside the
// hunk each sits on; a thread on a file the changeset does not hold is left out.
func TestAttachThreadsNamesEachThreadOnItsFile(t *testing.T) {
	rev := types.Diff{Files: []types.DiffFile{threadFile(), {Path: "docs/cache.md"}}}
	comments := []types.ReviewComment{
		{ID: "c1", Path: "internal/cache/cache.go", Line: 42, Author: "ana", Body: "?"},
		{ID: "c2", Root: "c1", Author: "ben", Body: "!"},
		{ID: "c3", Path: "internal/cache/cache.go", Line: 90, Outdated: true, Author: "ana", Body: "old"},
		{ID: "c9", Path: "elsewhere.go", Line: 1, Author: "ana", Body: "not here"},
	}

	AttachThreads(&rev, changeset.ParseHunks(threadPatch), comments)

	assert.Equal(t, []types.DiffThreadRef{
		{ID: "c1", Hunk: 1, Line: 42, Comments: 2},
		{ID: "c3", Hunk: -1, Line: 90, Comments: 1, Outdated: true},
	}, rev.Files[0].Threads)
	assert.Empty(t, rev.Files[1].Threads)
	assert.Equal(t, "thread c1, 2 comments", ThreadRefLine(rev.Files[0].Threads[0]))
	assert.Equal(t, "thread c3, 1 comment, outdated", ThreadRefLine(rev.Files[0].Threads[1]))
}

// NewThreadInput is the one assembly every transport goes through, so what it does with the
// pieces is pinned here: annotate only a patch that has hunks, hand it the paths in patch order,
// and say so when no notes store was wired.
func TestNewThreadInputAnnotatesOnlyAPatchWithHunks(t *testing.T) {
	var asked [][]string
	annotate := func(_ context.Context, paths []string) (types.Diff, error) {
		asked = append(asked, paths)
		return types.Diff{SeedProjects: []string{"cache"}}, nil
	}
	comments := []types.ReviewComment{{ID: "c1"}}

	empty, err := NewThreadInput(t.Context(), ThreadParts{Comments: comments, Annotate: annotate})
	require.NoError(t, err)
	assert.Empty(t, asked, "a review outlives the tree it was written on")
	assert.Equal(t, ThreadInput{
		Comments:      comments,
		AnchorsUnread: "this server has no notes store wired, so none was joined",
	}, empty)

	full, err := NewThreadInput(t.Context(), ThreadParts{Patch: threadPatch, Comments: comments, Annotate: annotate})
	require.NoError(t, err)
	assert.Equal(t, [][]string{{"internal/cache/cache.go"}}, asked)
	assert.Equal(t, []string{"cache"}, full.Changeset.SeedProjects)
	assert.Len(t, full.Hunks, 1)
}

func TestNewThreadInputReturnsWhatTheTransportCouldNotRead(t *testing.T) {
	boom := errors.New("boom")

	_, err := NewThreadInput(t.Context(), ThreadParts{
		Patch:    threadPatch,
		Annotate: func(context.Context, []string) (types.Diff, error) { return types.Diff{}, boom },
	})
	require.ErrorIs(t, err, boom)

	_, err = NewThreadInput(t.Context(), ThreadParts{
		Anchors: func(context.Context, types.Diff) ([]AnchorHit, error) { return nil, boom },
	})
	require.ErrorIs(t, err, boom, "a misdeclared notes store is a fault, not an omitted section")

	hit := AnchorHit{Note: "n"}
	in, err := NewThreadInput(t.Context(), ThreadParts{
		Anchors: func(context.Context, types.Diff) ([]AnchorHit, error) { return []AnchorHit{hit}, nil },
	})
	require.NoError(t, err)
	assert.Equal(t, []AnchorHit{hit}, in.Anchors)
	assert.Empty(t, in.AnchorsUnread)
}
