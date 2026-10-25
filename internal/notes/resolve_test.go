package notes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// givenAnchorsResolver resolves exactly the anchors it was given and nothing else, and reports
// whatever digest and signature it was told to.
type givenAnchorsResolver struct {
	live    map[string]bool
	digests map[string]string
	// sigs answers Signature. A symbol missing from it errors, the way a symbol whose
	// indexer rendered no signature does.
	sigs map[string]string
}

func (f givenAnchorsResolver) Resolves(_ context.Context, a Anchor) bool {
	return f.live[fmt.Sprintf("%s:%s", string(a.Kind), a.Target)]
}
func (f givenAnchorsResolver) Digest(_ context.Context, a Anchor) (string, error) {
	return f.digests[fmt.Sprintf("%s:%s", string(a.Kind), a.Target)], nil
}
func (f givenAnchorsResolver) Signature(_ context.Context, a Anchor) (string, error) {
	sig, ok := f.sigs[fmt.Sprintf("%s:%s", string(a.Kind), a.Target)]
	if !ok {
		return "", errors.New("the test indexer rendered no signature")
	}
	return sig, nil
}

func TestAnchorIssues(t *testing.T) {
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

	res := givenAnchorsResolver{live: map[string]bool{
		"file:internal/cache/cache.go": true,
	}}
	issues, err := AnchorIssues(context.Background(), dir, res)
	require.NoError(t, err)
	require.Len(t, issues, 1, "only the unresolved anchor is reported")

	got := issues[0]
	assert.Equal(t, Issue{
		Severity: SeverityWarning,
		Code:     CodeDanglingAnchor,
		Path:     got.Path,
		Note:     "pairing",
		Message:  got.Message, // asserted below
		Hint:     got.Hint,
	}, got, "a symbol disappears for ordinary reasons; the note is still worth reading, just not silently current")
	assert.Contains(t, got.Message, "m internal/cache/Store#Put().")
	assert.Contains(t, got.Hint, "magus refs", "the hint routes to the tool that finds the new name")
	assert.Contains(t, got.Hint, "Do NOT let a tool guess",
		"a low-confidence re-anchor is worse than an admitted failure")
}

func TestAnchorIssues_AllLiveReportsNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:    "fine",
		Title:   "Still true",
		Anchors: []Anchor{{Kind: AnchorProject, Target: "."}},
	}))

	issues, err := AnchorIssues(context.Background(), dir, givenAnchorsResolver{live: map[string]bool{"project:.": true}})
	require.NoError(t, err)
	assert.Empty(t, issues)
}

// TestAnchorIssues_NeverRewritesTheStore is the guard against the failure mode every
// system in this space eventually shipped: silently re-pointing an anchor at a best guess,
// which turns a detectable problem into a confident wrong answer.
func TestAnchorIssues_NeverRewritesTheStore(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	n := Note{
		Name:    "pairing",
		Title:   "Two caches",
		Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m gone/Removed#"}},
	}
	require.NoError(t, Save(dir, n))

	before, err := Get(dir, n.Name)
	require.NoError(t, err)

	_, err = AnchorIssues(context.Background(), dir, givenAnchorsResolver{})
	require.NoError(t, err)

	after, err := Get(dir, n.Name)
	require.NoError(t, err)
	assert.Equal(t, before, after, "verify reports; it never re-anchors, and it does not touch the file at all")
}

func TestDegradeHintNamesTheFallback(t *testing.T) {
	assert.Contains(t, degradeHint(Anchor{Kind: AnchorSymbol}), "degrades to its file")
	assert.Contains(t, degradeHint(Anchor{Kind: AnchorFile}), "degrades to its project")
	assert.Contains(t, degradeHint(Anchor{Kind: AnchorNote}), "note it points at is gone")
	assert.Contains(t, degradeHint(Anchor{Kind: AnchorProject}), "no longer exists")
}

// TestFileAnchorStillDriftsOnContent covers the case an existence check cannot see: the file
// is still there and quietly stopped saying what the note claims. Signature grading is for
// symbols; a file has no signature, so its whole content stays the fingerprint.
func TestFileAnchorStillDriftsOnContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:    "pairing",
		Title:   "Two caches",
		Anchors: []Anchor{{Kind: AnchorFile, Target: "internal/cache/cache.go", Digest: "aaaaaaaaaaaaaaaa"}},
	}))
	res := givenAnchorsResolver{
		live:    map[string]bool{"file:internal/cache/cache.go": true},
		digests: map[string]string{"file:internal/cache/cache.go": "bbbbbbbbbbbbbbbb"},
	}

	issues, err := AnchorIssues(context.Background(), dir, res)
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, CodeDriftedAnchor, issues[0].Code)
	assert.Contains(t, issues[0].Message, "still exists but its content changed")
	assert.Contains(t, issues[0].Hint, "Nothing re-records it for you")

	res.digests["file:internal/cache/cache.go"] = "aaaaaaaaaaaaaaaa"
	issues, err = AnchorIssues(context.Background(), dir, res)
	require.NoError(t, err)
	assert.Empty(t, issues, "content equal to the recorded digest is clean")
}

// TestAnchorIssuesSilentWhenADigestIsUnavailable pins the direction the tuning must
// fail in. An absent fingerprint is no opinion, never "changed": a gate that cries drift
// because it could not compute something is exactly the gate people stop reading.
func TestAnchorIssuesSilentWhenADigestIsUnavailable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:    "pairing",
		Title:   "Two caches",
		Anchors: []Anchor{{Kind: AnchorFile, Target: "cache.go", Digest: "aaaaaaaaaaaaaaaa"}},
	}))
	live := map[string]bool{"file:cache.go": true}

	issues, err := AnchorIssues(context.Background(), dir, givenAnchorsResolver{live: live}) // no digests at all
	require.NoError(t, err)
	assert.Empty(t, issues, "an uncomputable fingerprint is silence")

	// And a note that never recorded one is silent too, not retroactively drifted. Its own
	// directory, so the stamped note above cannot supply the finding.
	fresh := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(fresh, Note{
		Name:    "unstamped",
		Title:   "Never re-anchored",
		Anchors: []Anchor{{Kind: AnchorFile, Target: "cache.go"}},
	}))
	issues, err = AnchorIssues(context.Background(), fresh, givenAnchorsResolver{
		live:    live,
		digests: map[string]string{"file:cache.go": "bbbbbbbbbbbbbbbb"},
	})
	require.NoError(t, err)
	assert.Empty(t, issues, "a note with no recorded fingerprint is not retroactively drifted")
}

// TestRecordDigestsRecordsASymbolsSignature: a symbol anchor is re-attested by its signature,
// and a body digest left from before signature grading goes, since nothing reads it.
func TestRecordDigestsRecordsASymbolsSignature(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:    "pairing",
		Title:   "Two caches",
		Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m cache/Put().", Digest: "aaaaaaaaaaaaaaaa"}},
	}))
	res := givenAnchorsResolver{
		live: map[string]bool{"symbol:m cache/Put().": true},
		sigs: map[string]string{"symbol:m cache/Put().": "func Put(k string) error"},
	}

	changed, err := RecordDigests(context.Background(), dir, "pairing", "cafe1234", res)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	got, err := Get(dir, "pairing")
	require.NoError(t, err)
	assert.Equal(t, Anchor{Kind: AnchorSymbol, Target: "m cache/Put().", Signature: "func Put(k string) error", Commit: "cafe1234"},
		got.Anchors[0])

	// Idempotent: nothing to re-attest, nothing written.
	changed, err = RecordDigests(context.Background(), dir, "pairing", "cafe1234", res)
	require.NoError(t, err)
	assert.Equal(t, 0, changed)
}

// TestRecordDigestsKeepsASignatureItCannotRead: writing "" would erase the signature a person
// attested, and the note would read unverifiable for a reason nobody chose.
func TestRecordDigestsKeepsASignatureItCannotRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "notes")
	require.NoError(t, Save(dir, Note{
		Name:    "pairing",
		Title:   "Two caches",
		Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m cache/Put().", Signature: "func Put()"}},
	}))

	changed, err := RecordDigests(context.Background(), dir, "pairing", "cafe1234", givenAnchorsResolver{
		live: map[string]bool{"symbol:m cache/Put().": true},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, changed)

	got, err := Get(dir, "pairing")
	require.NoError(t, err)
	assert.Equal(t, "func Put()", got.Anchors[0].Signature)
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
	res := givenAnchorsResolver{
		live: map[string]bool{"symbol:m cache/Put().": true},
		sigs: map[string]string{"symbol:m cache/Put().": "func Put()"},
	}

	changed, err := RecordDigests(context.Background(), dir, "pairing", "cafe1234", res)
	require.NoError(t, err)
	assert.Equal(t, 1, changed)

	assert.NoFileExists(t, filepath.Join(dir, "pairing.md"), "re-attestation must not fork the note")
	found, _, err := Inspect(dir)
	require.NoError(t, err)
	require.Len(t, found, 1)
	assert.Equal(t, file, found[0].Path)
	assert.Equal(t, "func Put()", found[0].Anchors[0].Signature)
}

// A verify warning names the file to go and repair, so it has to be the file that exists.
func TestAnchorIssuesReportsTheRealFileAfterARename(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "Some Note.md")
	require.NoError(t, os.WriteFile(file,
		[]byte("---\nmagus:\n  id: pairing\n  title: Two caches\n  anchors:\n    - kind: symbol\n      target: m gone/Removed().\n---\n\nProse.\n"), 0o644))

	issues, err := AnchorIssues(context.Background(), dir, givenAnchorsResolver{})
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

	changed, err := RecordDigests(context.Background(), dir, "ghost", "cafe1234", givenAnchorsResolver{
		sigs: map[string]string{"symbol:m gone/Removed#": "type Removed struct{}"},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, changed)

	got, err := Get(dir, "ghost")
	require.NoError(t, err)
	assert.Empty(t, got.Anchors[0].Signature)
}

// TestRecordDigestsStampsTheRevision: the digest says the anchored code changed, and the
// commit says what to diff it against. They are written together because a pair that
// disagreed would send a reader to the wrong diff, which is worse than no provenance, and
// the CLI has always printed this field, so leaving nothing writing it meant printing blank.
func TestRecordDigestsStampsTheRevision(t *testing.T) {
	dir := t.TempDir()
	n := Scaffold("stamped")
	n.Anchors = []Anchor{{Kind: AnchorFile, Target: "a.go"}}
	require.NoError(t, Save(dir, n))

	changed, err := RecordDigests(context.Background(), dir, "stamped", "cafe1234", givenAnchorsResolver{
		live:    map[string]bool{"file:a.go": true},
		digests: map[string]string{"file:a.go": "d1"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, changed)

	got, err := Get(dir, "stamped")
	require.NoError(t, err)
	require.Len(t, got.Anchors, 1)
	assert.Equal(t, Anchor{Kind: AnchorFile, Target: "a.go", Digest: "d1", Commit: "cafe1234"}, got.Anchors[0],
		"the digest's provenance travels with it")

	// The drift report is what the provenance is FOR: naming the diff turns "something
	// changed" into something a reader can act on without going looking.
	issues, err := AnchorIssues(context.Background(), dir, givenAnchorsResolver{
		live:    map[string]bool{"file:a.go": true},
		digests: map[string]string{"file:a.go": "d2"},
	})
	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, CodeDriftedAnchor, issues[0].Code)
	assert.Contains(t, issues[0].Hint, "git diff cafe1234..HEAD -- a.go")
}

// A Go struct's signature carries its fields, so a long one is where a cap anywhere between
// the indexer and the grade would hide a change. The field that changes sits past 4 KiB.
func longStructSignature(last string) string {
	var b strings.Builder
	b.WriteString("type Wide struct {")
	for i := range 300 {
		fmt.Fprintf(&b, " Field%03d string;", i)
	}
	fmt.Fprintf(&b, " %s }", last)
	return b.String()
}

func TestSymbolAnchorDriftsOnASignatureChange(t *testing.T) {
	require.Greater(t, len(longStructSignature("Last int")), 4096)
	for _, tt := range []struct{ name, recorded, current string }{
		{"a parameter", "func Put(k string) error", "func Put(k string, ttl time.Duration) error"},
		{"a struct field past the cap", longStructSignature("Last int"), longStructSignature("Last int64")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "notes")
			require.NoError(t, Save(dir, Note{
				Name:    "pairing",
				Title:   "Two caches",
				Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m cache/Put().", Signature: tt.recorded, Commit: "cafe1234"}},
			}))
			res := givenAnchorsResolver{
				live: map[string]bool{"symbol:m cache/Put().": true},
				sigs: map[string]string{"symbol:m cache/Put().": tt.current},
			}

			issues, err := AnchorIssues(t.Context(), dir, res)
			require.NoError(t, err)
			require.Len(t, issues, 1)
			assert.Equal(t, CodeDriftedAnchor, issues[0].Code)
			assert.Contains(t, issues[0].Message, "its signature changed")
			assert.Contains(t, issues[0].Hint, "last reviewed at cafe1234")
		})
	}
}

// TestSymbolAnchorWithoutASignatureIsUnverifiable: neither a note recorded before signature
// grading nor a symbol its indexer renders no signature for may read clean, and neither is
// drift: nothing compared anything.
func TestSymbolAnchorWithoutASignatureIsUnverifiable(t *testing.T) {
	for _, tt := range []struct {
		name, recorded string
		current        map[string]string
		message        string
	}{
		{"none recorded", "", map[string]string{"symbol:m cache/Put().": "func Put()"}, "no recorded signature"},
		{"none rendered", "func Put()", nil, "the test indexer rendered no signature"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "notes")
			require.NoError(t, Save(dir, Note{
				Name:    "pairing",
				Title:   "Two caches",
				Anchors: []Anchor{{Kind: AnchorSymbol, Target: "m cache/Put().", Digest: "oldbody", Signature: tt.recorded}},
			}))
			res := givenAnchorsResolver{
				live: map[string]bool{"symbol:m cache/Put().": true},
				sigs: tt.current,
			}

			issues, err := AnchorIssues(t.Context(), dir, res)
			require.NoError(t, err)
			require.Len(t, issues, 1)
			assert.Equal(t, CodeUnverifiableAnchor, issues[0].Code)
			assert.Contains(t, issues[0].Message, tt.message)
		})
	}

	hint := gradeSignature(t.Context(), givenAnchorsResolver{}, "pairing", Anchor{Kind: AnchorSymbol, Target: "m cache/Put()."}).hint
	assert.Contains(t, hint, "recorded before notes graded symbols by signature")
	assert.Contains(t, hint, "`magus notes edit pairing` records it")
}

// gradedStore writes one note per grading outcome, so a note NAME identifies the expected
// verdict without any test having to read a verdict back out of prose.
func gradedStore(t *testing.T) (string, givenAnchorsResolver) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "notes")
	for _, n := range []Note{
		{Name: "gone", Title: "Points at nothing", Anchors: []Anchor{
			{Kind: AnchorSymbol, Target: "m gone/Removed#"},
		}},
		{Name: "drifted", Title: "Moved on", Anchors: []Anchor{
			{Kind: AnchorFile, Target: "internal/cache/cache.go", Digest: "oldbody"},
		}},
		{Name: "resigned", Title: "New signature", Anchors: []Anchor{
			{Kind: AnchorSymbol, Target: "m cache/Put().", Signature: "func Put()"},
		}},
		{Name: "unrecorded", Title: "Before signatures", Anchors: []Anchor{
			{Kind: AnchorSymbol, Target: "m cache/Get().", Digest: "oldbody"},
		}},
		{Name: "clean", Title: "Still true", Anchors: []Anchor{
			{Kind: AnchorFile, Target: "clean.go"},
			{Kind: AnchorSymbol, Target: "m clean/Ok().", Signature: "func Ok() bool"},
		}},
	} {
		require.NoError(t, Save(dir, n))
	}

	return dir, givenAnchorsResolver{
		live: map[string]bool{
			"file:internal/cache/cache.go": true,
			"symbol:m cache/Put().":        true,
			"symbol:m cache/Get().":        true,
			"file:clean.go":                true,
			"symbol:m clean/Ok().":         true,
		},
		digests: map[string]string{"file:internal/cache/cache.go": "newbody"},
		sigs: map[string]string{
			"symbol:m cache/Put().": "func Put(k string)",
			"symbol:m cache/Get().": "func Get() string",
			"symbol:m clean/Ok().":  "func Ok() bool",
		},
	}
}

// TestResolveAnchorsSeesEveryAnchor covers what the Issue view structurally cannot: a
// healthy anchor, which is most of them and most of what a diff touches.
func TestResolveAnchorsSeesEveryAnchor(t *testing.T) {
	dir, res := gradedStore(t)

	all, err := ResolveAnchors(t.Context(), dir, res)
	require.NoError(t, err)
	require.Len(t, all, 6, "every anchor of every note, healthy ones included")

	byNote := map[string][]ResolvedAnchor{}
	for _, r := range all {
		byNote[r.Note] = append(byNote[r.Note], r)
	}
	for note, want := range map[string]IssueCode{
		"gone":       CodeDanglingAnchor,
		"drifted":    CodeDriftedAnchor,
		"resigned":   CodeDriftedAnchor,
		"unrecorded": CodeUnverifiableAnchor,
	} {
		require.Len(t, byNote[note], 1)
		assert.Equal(t, want, byNote[note][0].Status, note)
	}

	clean := byNote["clean"]
	require.Len(t, clean, 2)
	assert.Equal(t, ResolvedAnchor{
		Note:   "clean",
		Title:  "Still true", // a hit renders far from the store, so the title rides along
		Pos:    0,
		Anchor: Anchor{Kind: AnchorFile, Target: "clean.go"},
		File:   "clean.go", // a file anchor already knows its file
	}, clean[0])
	for i, r := range clean {
		assert.Empty(t, r.Status, "an anchor nothing is wrong with reports no code")
		assert.Equal(t, i, r.Pos, "position is the anchor's index in its own note")
	}
	assert.Empty(t, clean[1].File,
		"a symbol key names a package and a descriptor, never a path; only the graph knows where it sits")
}

// TestResolveAnchorsAgreesWithAnchorIssues is the reason the two APIs share a grader. A
// client whose verdict disagreed with `magus notes verify` would be a second opinion rather
// than a second view of one answer, and nothing in the types would catch the divergence.
func TestResolveAnchorsAgreesWithAnchorIssues(t *testing.T) {
	dir, res := gradedStore(t)

	issues, err := AnchorIssues(t.Context(), dir, res)
	require.NoError(t, err)
	all, err := ResolveAnchors(t.Context(), dir, res)
	require.NoError(t, err)

	// Keyed by note because the Issue view identifies an anchor only inside a message written
	// for a person, which is itself the reason the per-anchor view had to exist.
	reported := map[string][]IssueCode{}
	for _, i := range issues {
		reported[i.Note] = append(reported[i.Note], i.Code)
	}
	graded := map[string][]IssueCode{}
	for _, r := range all {
		if r.Status != "" {
			graded[r.Note] = append(graded[r.Note], r.Status)
		}
	}
	for _, m := range []map[string][]IssueCode{reported, graded} {
		for _, codes := range m {
			slices.Sort(codes)
		}
	}
	assert.Equal(t, reported, graded)
}

// The join is what the per-anchor view exists to feed, and a drift finding has to survive the
// trip: AnchorHits reports what a diff touches and never re-grades.
func TestResolveAnchorsDrivesTheDiffJoin(t *testing.T) {
	dir, res := gradedStore(t)

	all, err := ResolveAnchors(t.Context(), dir, res)
	require.NoError(t, err)

	hits := AnchorHits(all, []string{"internal/cache/cache.go"}, nil)
	require.Len(t, hits, 1)
	assert.Equal(t, AnchorHit{
		Note: "drifted", Title: "Moved on", Pos: 0,
		Kind: AnchorFile, Target: "internal/cache/cache.go",
		Matched: "internal/cache/cache.go", Match: MatchFile,
		Status: CodeDriftedAnchor,
	}, hits[0])
}

// TestResolveAnchorsUngradedWithoutAResolver pins what the nil resolver buys: a caller whose
// knowledge graph will not load still learns WHAT is anchored, and every anchor says so rather
// than reporting the "" a graded pass reserves for anchors it checked and found clean.
func TestResolveAnchorsUngradedWithoutAResolver(t *testing.T) {
	dir, _ := gradedStore(t)

	all, err := ResolveAnchors(t.Context(), dir, nil)
	require.NoError(t, err)
	require.Len(t, all, 6, "an ungraded pass sees exactly the anchors a graded one does")

	for _, r := range all {
		assert.Equal(t, StatusUngraded, r.Status, "note %s anchor %d", r.Note, r.Pos)
	}
}

// TestUngradedResolutionDiffersOnlyInStatus: the nil-resolver path is the same walk, not a
// second one. Fewer anchors, a different order, or an unpopulated File would make a drift
// column outage read as a change in what the store is about.
func TestUngradedResolutionDiffersOnlyInStatus(t *testing.T) {
	dir, res := gradedStore(t)

	graded, err := ResolveAnchors(t.Context(), dir, res)
	require.NoError(t, err)
	ungraded, err := ResolveAnchors(t.Context(), dir, nil)
	require.NoError(t, err)

	require.Len(t, ungraded, len(graded))
	for i, want := range graded {
		want.Status = StatusUngraded
		assert.Equal(t, want, ungraded[i])
	}
}
