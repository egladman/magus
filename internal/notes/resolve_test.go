package notes

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeResolver resolves exactly the anchors it was given and nothing else, and reports
// whatever digest it was told to.
type fakeResolver struct {
	live    map[string]bool
	digests map[string]string
}

func (f fakeResolver) Resolves(_ context.Context, a Anchor) bool {
	return f.live[string(a.Kind)+":"+a.Target]
}
func (f fakeResolver) Digest(_ context.Context, a Anchor) (string, error) {
	return f.digests[string(a.Kind)+":"+a.Target], nil
}

func TestResolveAnchors(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:  "pairing",
		Title: "Two caches",
		Anchors: []Anchor{
			{Kind: AnchorSymbol, Target: "m internal/cache/Store#Put()."},
			{Kind: AnchorFile, Target: "internal/cache/cache.go"},
		},
		Body: "prose",
	}))

	res := fakeResolver{live: map[string]bool{
		"file:internal/cache/cache.go": true,
	}}
	issues, err := ResolveAnchors(context.Background(), dir, res)
	require.NoError(t, err)
	require.Len(t, issues, 1, "only the unresolved anchor is reported")

	got := issues[0]
	assert.Equal(t, SeverityWarning, got.Severity,
		"a symbol disappears for ordinary reasons; the note is still worth reading, just not silently current")
	assert.Equal(t, CodeDanglingAnchor, got.Code)
	assert.Equal(t, "pairing", got.Note)
	assert.Contains(t, got.Message, "m internal/cache/Store#Put().")
	assert.Contains(t, got.Hint, "magus refs", "the hint routes to the tool that finds the new name")
	assert.Contains(t, got.Hint, "Do NOT let a tool guess",
		"a low-confidence re-anchor is worse than an admitted failure")
}

func TestResolveAnchors_AllLiveReportsNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:    "fine",
		Title:   "Still true",
		Anchors: []Anchor{{Kind: AnchorProject, Target: "."}},
	}))

	issues, err := ResolveAnchors(context.Background(), dir, fakeResolver{live: map[string]bool{"project:.": true}})
	require.NoError(t, err)
	assert.Empty(t, issues)
}

// TestResolveAnchors_NeverRewritesTheStore is the guard against the failure mode every
// system in this space eventually shipped: silently re-pointing an anchor at a best guess,
// which turns a detectable problem into a confident wrong answer.
func TestResolveAnchors_NeverRewritesTheStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	n := Note{
		Name:    "pairing",
		Title:   "Two caches",
		Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m gone/Removed#"}},
	}
	require.NoError(t, Save(dir, n))

	before, err := Get(dir, n.Name)
	require.NoError(t, err)

	_, err = ResolveAnchors(context.Background(), dir, fakeResolver{})
	require.NoError(t, err)

	after, err := Get(dir, n.Name)
	require.NoError(t, err)
	assert.Equal(t, before.Anchors, after.Anchors, "verify reports; it never re-anchors")
	assert.Equal(t, before.Modified, after.Modified, "and it does not touch the file at all")
}

func TestDegradeHintNamesTheFallback(t *testing.T) {
	assert.Contains(t, degradeHint(Anchor{Kind: AnchorSymbol}), "degrades to its file")
	assert.Contains(t, degradeHint(Anchor{Kind: AnchorFile}), "degrades to its project")
	assert.Contains(t, degradeHint(Anchor{Kind: AnchorNote}), "note it points at is gone")
	assert.Contains(t, degradeHint(Anchor{Kind: AnchorProject}), "no longer exists")
}

// TestResolveAnchorsReportsDrift covers the case an existence check cannot see: the code
// is still there and quietly stopped saying what the note claims.
func TestResolveAnchorsReportsDrift(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:    "pairing",
		Title:   "Two caches",
		Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m cache/Put().", Digest: "aaaaaaaaaaaaaaaa"}},
	}))
	res := fakeResolver{
		live:    map[string]bool{"symbol:m cache/Put().": true},
		digests: map[string]string{"symbol:m cache/Put().": "bbbbbbbbbbbbbbbb"},
	}

	issues, err := ResolveAnchors(context.Background(), dir, res)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, CodeDriftedAnchor, issues[0].Code)
	assert.Contains(t, issues[0].Message, "still exists but its content changed")
	assert.Contains(t, issues[0].Hint, "Nothing re-records it for you")
}

// TestResolveAnchorsSilentWhenADigestIsUnavailable pins the direction the tuning must
// fail in. An absent fingerprint is no opinion, never "changed": a gate that cries drift
// because it could not compute something is exactly the gate people stop reading.
func TestResolveAnchorsSilentWhenADigestIsUnavailable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:    "pairing",
		Title:   "Two caches",
		Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m cache/Put().", Digest: "aaaaaaaaaaaaaaaa"}},
	}))
	live := map[string]bool{"symbol:m cache/Put().": true}

	issues, err := ResolveAnchors(context.Background(), dir, fakeResolver{live: live}) // no digests at all
	require.NoError(t, err)
	assert.Empty(t, issues, "an uncomputable fingerprint is silence")

	// And a note that never recorded one is silent too, not retroactively drifted. Its own
	// directory, so the stamped note above cannot supply the finding.
	fresh := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(fresh, Note{
		Name:    "unstamped",
		Title:   "Never re-anchored",
		Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m cache/Put()."}},
	}))
	issues, err = ResolveAnchors(context.Background(), fresh, fakeResolver{
		live:    live,
		digests: map[string]string{"symbol:m cache/Put().": "bbbbbbbbbbbbbbbb"},
	})
	require.NoError(t, err)
	assert.Empty(t, issues, "a note with no recorded fingerprint is not retroactively drifted")
}

func TestRecordDigestsIsDeliberateAndReportsWhatItDid(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:    "pairing",
		Title:   "Two caches",
		Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m cache/Put().", Digest: "aaaaaaaaaaaaaaaa"}},
	}))
	res := fakeResolver{
		live:    map[string]bool{"symbol:m cache/Put().": true},
		digests: map[string]string{"symbol:m cache/Put().": "bbbbbbbbbbbbbbbb"},
	}

	changed, err := RecordDigests(context.Background(), dir, "pairing", "cafe1234", res)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	got, err := Get(dir, "pairing")
	require.NoError(t, err)
	assert.Equal(t, "bbbbbbbbbbbbbbbb", got.Anchors[0].Digest)

	// Idempotent: nothing to re-attest, nothing written.
	changed, err = RecordDigests(context.Background(), dir, "pairing", "cafe1234", res)
	require.NoError(t, err)
	assert.Equal(t, 0, changed)
}

// TestRecordDigestsSkipsADanglingAnchor: a fingerprint for something that no longer exists
// would be a lie, and would silence the dangling report on the next verify.
func TestRecordDigestsSkipsADanglingAnchor(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:    "ghost",
		Title:   "Points at nothing",
		Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m gone/Removed#"}},
	}))

	changed, err := RecordDigests(context.Background(), dir, "ghost", "cafe1234", fakeResolver{
		digests: map[string]string{"symbol:m gone/Removed#": "cccccccccccccccc"},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, changed)

	got, err := Get(dir, "ghost")
	require.NoError(t, err)
	assert.Empty(t, got.Anchors[0].Digest)
}

// TestRecordDigestsStampsTheRevision: the digest says the anchored code changed, and the
// commit says what to diff it against. They are written together because a pair that
// disagreed would send a reader to the wrong diff, which is worse than no provenance - and
// the CLI has always printed this field, so leaving nothing writing it meant printing blank.
func TestRecordDigestsStampsTheRevision(t *testing.T) {
	dir := t.TempDir()
	n := Scaffold("stamped")
	n.Anchors = []Anchor{{Kind: AnchorFile, Target: "a.go"}}
	require.NoError(t, Save(dir, n))

	changed, err := RecordDigests(context.Background(), dir, "stamped", "cafe1234", fakeResolver{
		live:    map[string]bool{"file:a.go": true},
		digests: map[string]string{"file:a.go": "d1"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, changed)

	got, err := Get(dir, "stamped")
	require.NoError(t, err)
	require.Len(t, got.Anchors, 1)
	assert.Equal(t, "d1", got.Anchors[0].Digest)
	assert.Equal(t, "cafe1234", got.Anchors[0].Commit, "the digest's provenance travels with it")

	// The drift report is what the provenance is FOR: naming the diff turns "something
	// changed" into something a reader can act on without going looking.
	issues, err := ResolveAnchors(context.Background(), dir, fakeResolver{
		live:    map[string]bool{"file:a.go": true},
		digests: map[string]string{"file:a.go": "d2"},
	})
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, CodeDriftedAnchor, issues[0].Code)
	assert.Contains(t, issues[0].Hint, "git diff cafe1234..HEAD -- a.go")
}
