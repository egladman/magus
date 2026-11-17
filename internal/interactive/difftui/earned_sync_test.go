package difftui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/changeset"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/types/vcs"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types/diff"
)

// recordingSync is the inner sync EarnedSync wraps, so a test can assert the wrapper still
// forwards every mark: the server and the console read the reader's progress through it, and
// a wrapper that swallowed marks would desynchronize them silently.
type recordingSync struct {
	marks  []string
	seen   []string
	closed bool
}

func (r *recordingSync) SetCursor(diff.Cursor) {}

func (r *recordingSync) SetViewed(digest string, on bool) { r.marks = append(r.marks, digest) }

func (r *recordingSync) SetThreadsSeen(ids []string) { r.seen = append(r.seen, ids...) }

func (r *recordingSync) Close() { r.closed = true }

func earnedFixture(t *testing.T, files map[string]string) (root, cache string, tui []File) {
	t.Helper()
	root, cache = t.TempDir(), t.TempDir()
	for path, body := range files {
		require.NoError(t, os.WriteFile(filepath.Join(root, path), []byte(body), 0o644))
	}
	return root, cache, tui
}

func tuiFile(path string, generated bool, digests ...string) File {
	f := File{Path: path, Generated: generated}
	for _, d := range digests {
		f.Hunks = append(f.Hunks, Hunk{Digest: d})
	}
	return f
}

func TestEarnedSyncMintsWhenEveryHunkIsMarked(t *testing.T) {
	root, cache, _ := earnedFixture(t, map[string]string{"a.go": "package a\n"})
	inner := &recordingSync{}
	e := NewEarnedSync(inner, workingContent(root), cache, []File{tuiFile("a.go", false, "h1", "h2")}, nil)

	e.SetViewed("h1", true)
	e.Close()
	store, err := review.Load(cache)
	require.NoError(t, err)
	assert.Empty(t, store, "one of two hunks read is not a file read")

	e = NewEarnedSync(&recordingSync{}, workingContent(root), cache, []File{tuiFile("a.go", false, "h1", "h2")}, nil)
	e.SetViewed("h1", true)
	e.SetViewed("h2", true)
	e.Close()
	store, err = review.Load(cache)
	require.NoError(t, err)
	assert.True(t, store.Covers("a.go", review.DigestFile(filepath.Join(root, "a.go"))))
}

// THE trust property. The seeded viewed set comes from an unauthenticated JSON file whose
// hunk digests are computable from `magus diff` output, so anything with write access can
// forge a complete reading. Without the live-mark rule, opening the viewer once would launder
// that forgery into durable receipts.
func TestEarnedSyncRefusesToMintFromASeededSetAlone(t *testing.T) {
	root, cache, _ := earnedFixture(t, map[string]string{"a.go": "package a\n"})
	// Every hunk already "read", exactly as a forged store would present them.
	e := NewEarnedSync(&recordingSync{}, workingContent(root), cache,
		[]File{tuiFile("a.go", false, "h1", "h2")}, []string{"h1", "h2"})
	e.Close()

	store, err := review.Load(cache)
	require.NoError(t, err)
	assert.Empty(t, store, "a seeded set with no live mark must mint nothing")
}

// ...but the seed still does its real job: a reader who finishes a file across two sittings
// earns it on the mark that completes it.
func TestEarnedSyncLetsASeededSetFinishALiveReading(t *testing.T) {
	root, cache, _ := earnedFixture(t, map[string]string{"a.go": "package a\n"})
	e := NewEarnedSync(&recordingSync{}, workingContent(root), cache,
		[]File{tuiFile("a.go", false, "h1", "h2")}, []string{"h1"})

	e.SetViewed("h2", true)
	e.Close()

	store, err := review.Load(cache)
	require.NoError(t, err)
	assert.True(t, store.Covers("a.go", review.DigestFile(filepath.Join(root, "a.go"))),
		"the last hunk was marked in this session, so the reading was earned")
}

// Reading a machine's restatement of an edit made elsewhere is not the review, which is why
// the file list folds generated output away by default too.
func TestEarnedSyncIgnoresGeneratedFiles(t *testing.T) {
	root, cache, _ := earnedFixture(t, map[string]string{"gen.json": "{}\n"})
	e := NewEarnedSync(&recordingSync{}, workingContent(root), cache, []File{tuiFile("gen.json", true, "g1")}, nil)

	e.SetViewed("g1", true)
	e.Close()

	store, err := review.Load(cache)
	require.NoError(t, err)
	assert.Empty(t, store)
}

// The wrapper is a side channel, not a replacement: the server and the console read the
// reader's progress through the inner sync, and swallowing a mark would desynchronize them.
func TestEarnedSyncForwardsEveryMarkAndClosesTheInnerSync(t *testing.T) {
	root, cache, _ := earnedFixture(t, map[string]string{"a.go": "package a\n"})
	inner := &recordingSync{}
	e := NewEarnedSync(inner, workingContent(root), cache, []File{tuiFile("a.go", false, "h1")}, nil)

	e.SetViewed("h1", true)
	e.SetViewed("unknown-digest", true)
	e.Close()

	assert.Equal(t, []string{"h1", "unknown-digest"}, inner.marks)
	assert.True(t, inner.closed, "the wrapped sync must still be shut down")
}

// A file that cannot be fingerprinted records nothing rather than a receipt against no
// content, which Covers would then satisfy for every unreadable file forever.
func TestEarnedSyncMintsNothingForAFileItCannotRead(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	e := NewEarnedSync(&recordingSync{}, workingContent(root), cache, []File{tuiFile("gone.go", false, "h1")}, nil)
	e.now = func() time.Time { return time.Unix(0, 0) }

	e.SetViewed("h1", true)
	e.Close()

	store, err := review.Load(cache)
	require.NoError(t, err)
	assert.Empty(t, store)
}

// The terminal writes the watermark through the SAME store call the console's session route
// makes. Until it did, a reader who works entirely in `magus diff` marked nothing seen, and
// check-review reads an empty watermark as a workspace nobody has ever reviewed in and returns
// without asking the forge anything, so the bell could never ring for them at all.
func TestTerminalSeenMarkingReachesTheStoreTheConsoleWrites(t *testing.T) {
	root, cache := t.TempDir(), t.TempDir()
	sessions := changeset.NewStore(cache)
	sessions.Attach(root, "main", diff.Diff{Base: "main"}, "asof")

	StoreSync{store: sessions, root: root}.SetThreadsSeen([]string{"t1", "t2"})

	// Read back through a SECOND store, because the watermark has to survive the process: one
	// that lived in memory marks the whole conversation new again the next morning.
	seen, err := changeset.NewStore(cache).LoadSeenComments()
	require.NoError(t, err)
	assert.Equal(t, []string{"t1", "t2"}, seen)
}

// workingContent is the content a working-tree review attests to: the files under root.
func workingContent(root string) review.Content {
	return review.ReadContent(context.Background(), rootOnly(root), review.Source{})
}

// rootOnly is a ContentSource for a working-tree review, which reads nothing at a revision.
type rootOnly string

func (r rootOnly) Root() string { return string(r) }

func (rootOnly) FileAt(context.Context, string, string) (string, error) { return "", nil }

func (rootOnly) RevisionCheckpoint(context.Context, string) (vcs.Checkpoint, error) {
	return vcs.Checkpoint{}, nil
}
