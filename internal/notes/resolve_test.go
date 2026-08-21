package notes

import (
	"context"
	"os"
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
	// decls is left nil by most cases on purpose: an anchor with no current declaration
	// digest is UNGRADED, which is the path every note stamped before grading takes.
	decls map[string]string
}

func (f fakeResolver) Resolves(_ context.Context, a Anchor) bool {
	return f.live[string(a.Kind)+":"+a.Target]
}
func (f fakeResolver) Digest(_ context.Context, a Anchor) (string, error) {
	return f.digests[string(a.Kind)+":"+a.Target], nil
}
func (f fakeResolver) DeclDigest(_ context.Context, a Anchor) (string, error) {
	return f.decls[string(a.Kind)+":"+a.Target], nil
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

// Re-attestation on a note whose declared id no longer matches its filename. Saving to
// dir/<id>.md would leave the author with two notes and the original still unfingerprinted,
// which is the failure this whole store exists to avoid: a flag cleared without anyone
// reading the prose, on a file nobody was looking at.
func TestRecordDigestsUpdatesTheNoteInPlaceAfterARename(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "Some Note.md")
	require.NoError(t, os.WriteFile(file,
		[]byte("---\nmagus:\n  id: pairing\n  title: Two caches\n  anchors:\n    - kind: symbol\n      target: m cache/Put().\n---\n\nProse.\n"), 0o644))
	res := fakeResolver{
		live:    map[string]bool{"symbol:m cache/Put().": true},
		digests: map[string]string{"symbol:m cache/Put().": "bbbbbbbbbbbbbbbb"},
	}

	changed, err := RecordDigests(context.Background(), dir, "pairing", "cafe1234", res)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	assert.NoFileExists(t, filepath.Join(dir, "pairing.md"), "re-attestation must not fork the note")
	found, _, err := Inspect(dir)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, file, found[0].Path)
	assert.Equal(t, "bbbbbbbbbbbbbbbb", found[0].Anchors[0].Digest)
}

// A verify warning names the file to go and repair, so it has to be the file that exists.
func TestResolveAnchorsReportsTheRealFileAfterARename(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "Some Note.md")
	require.NoError(t, os.WriteFile(file,
		[]byte("---\nmagus:\n  id: pairing\n  title: Two caches\n  anchors:\n    - kind: symbol\n      target: m gone/Removed().\n---\n\nProse.\n"), 0o644))

	issues, err := ResolveAnchors(context.Background(), dir, fakeResolver{})
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, file, issues[0].Path)
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

// Grading is the answer to a measured base rate: only about one code change in thirty-eight
// to a documented subject actually invalidates the prose, and a whole-body hash fires on all
// thirty-eight. A body edited under an unchanged declaration is the common, mostly-harmless
// case, and reporting it as drift is how a store trains its reader to ignore the findings.
func TestBodyChangeUnderAnUnchangedDeclarationIsNotDrift(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:  "pairing",
		Title: "Two caches",
		Anchors: []Anchor{{
			Kind: AnchorSymbol, Target: "m internal/cache/Store#Put().",
			Digest: "oldbody", DeclDigest: "decl",
		}},
		Body: "prose",
	}))
	res := fakeResolver{
		live:    map[string]bool{"symbol:m internal/cache/Store#Put().": true},
		digests: map[string]string{"symbol:m internal/cache/Store#Put().": "newbody"},
		decls:   map[string]string{"symbol:m internal/cache/Store#Put().": "decl"},
	}

	issues, err := ResolveAnchors(t.Context(), dir, res)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, CodeAnchorBodyChanged, issues[0].Code)
	assert.Contains(t, issues[0].Message, "unchanged declaration")
}

func TestDeclarationChangeIsStillDrift(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:  "pairing",
		Title: "Two caches",
		Anchors: []Anchor{{
			Kind: AnchorSymbol, Target: "m internal/cache/Store#Put().",
			Digest: "oldbody", DeclDigest: "olddecl",
		}},
		Body: "prose",
	}))
	res := fakeResolver{
		live:    map[string]bool{"symbol:m internal/cache/Store#Put().": true},
		digests: map[string]string{"symbol:m internal/cache/Store#Put().": "newbody"},
		decls:   map[string]string{"symbol:m internal/cache/Store#Put().": "newdecl"},
	}

	issues, err := ResolveAnchors(t.Context(), dir, res)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, CodeDriftedAnchor, issues[0].Code, "what the subject IS changed")
}

// A note stamped before grading existed has no recorded declaration, and an anchor kind with
// no declaration never gets one. Neither may be silently downgraded to the softer finding:
// ungraded means the evidence to grade it is absent, not that the change was harmless.
func TestAnUngradedAnchorReportsDrift(t *testing.T) {
	for _, tt := range []struct {
		name       string
		declDigest string
		current    map[string]string
	}{
		{"note predates grading", "", map[string]string{"symbol:m internal/cache/Store#Put().": "decl"}},
		{"kind has no declaration", "decl", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "notes")
			require.NoError(t, Save(dir, Note{
				Name:  "pairing",
				Title: "Two caches",
				Anchors: []Anchor{{
					Kind: AnchorSymbol, Target: "m internal/cache/Store#Put().",
					Digest: "oldbody", DeclDigest: tt.declDigest,
				}},
				Body: "prose",
			}))
			res := fakeResolver{
				live:    map[string]bool{"symbol:m internal/cache/Store#Put().": true},
				digests: map[string]string{"symbol:m internal/cache/Store#Put().": "newbody"},
				decls:   tt.current,
			}

			issues, err := ResolveAnchors(t.Context(), dir, res)
			require.NoError(t, err)
			require.Len(t, issues, 1)
			assert.Equal(t, CodeDriftedAnchor, issues[0].Code)
		})
	}
}

// errDeclResolver computes a body digest but fails on the declaration - a symbol the indexer
// found but reported no enclosing range for.
type errDeclResolver struct{ fakeResolver }

func (errDeclResolver) DeclDigest(context.Context, Anchor) (string, error) {
	return "", assert.AnError
}

// TestDeclarationHeldFailsTowardsUngraded pins the DIRECTION the grading fails in, which is
// the whole reason it is one exported function rather than a rule each caller re-implements.
// Every way of being unsure - nothing recorded, nothing computable, an error - answers false,
// so the finding degrades to ungraded drift rather than to the softer verdict. Overstating a
// change costs a re-read; understating one is a note that quietly stopped being true.
func TestDeclarationHeldFailsTowardsUngraded(t *testing.T) {
	const target = "m internal/cache/Store#Put()."
	key := "symbol:" + target

	for _, tt := range []struct {
		name     string
		recorded string
		res      Resolver
		want     bool
	}{
		{"nothing recorded", "", fakeResolver{decls: map[string]string{key: "decl"}}, false},
		{"nothing computable", "decl", fakeResolver{}, false},
		{"declaration errored", "decl", errDeclResolver{}, false},
		{"declaration moved", "decl", fakeResolver{decls: map[string]string{key: "other"}}, false},
		{"declaration held", "decl", fakeResolver{decls: map[string]string{key: "decl"}}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a := Anchor{Kind: AnchorSymbol, Target: target, Digest: "oldbody", DeclDigest: tt.recorded}
			assert.Equal(t, tt.want, DeclarationHeld(t.Context(), tt.res, a))
		})
	}
}
