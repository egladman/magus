package symbols

import (
	"context"
	"errors"
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// occAt builds a reference occurrence of monikerV1 spanning (line, col) to (line, col+n),
// in SCIP's own 0-based coordinates, so the tests state positions the way the index does
// and the 1-based conversion stays visible as a conversion.
func occAt(line, col, n int) *scip.Occurrence {
	r := scip.Range{
		Start: scip.Position{Line: int32(line), Character: int32(col)},
		End:   scip.Position{Line: int32(line), Character: int32(col + n)},
	}
	return &scip.Occurrence{Symbol: monikerV1, TypedRange: r.AsTypedRange()}
}

func readerFor(files map[string]string) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		data, ok := files[p]
		if !ok {
			return nil, errors.New("no such file")
		}
		return []byte(data), nil
	}
}

// TestParseOccurrencesIsUncapped is the whole reason this path exists beside ParseIndex.
// The graph edge caps its line list at MaxRefLines so a hot symbol cannot bloat a shard;
// a rewrite driven off that would silently skip every site past the cap. Well past it
// here, so the assertion fails if the cap is ever applied on this path too.
func TestParseOccurrencesIsUncapped(t *testing.T) {
	const total = MaxRefLines*3 + 7
	doc := &scip.Document{RelativePath: "pkg/baz/baz.go"}
	for i := range total {
		doc.Occurrences = append(doc.Occurrences, occAt(i, 0, 3))
	}
	idx := &scip.Index{Documents: []*scip.Document{doc}}

	files, _, err := ParseOccurrences(t.Context(), marshalIndex(t, idx), "", "gomod example.com/foo Bar#")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Len(t, files[0].Occurrences, total,
		"every occurrence must survive: MaxRefLines is the graph edge's cap, not this path's")
}

// TestParseOccurrencesRangesRolesAndOrder pins the three things a mechanical edit depends
// on: 1-based positions (an off-by-one lands every replacement one character early), the
// definition flag (a rename touches it, a call-site-only pass does not), and position
// order within a file.
func TestParseOccurrencesRangesRolesAndOrder(t *testing.T) {
	def := occAt(10, 5, 3)
	def.SymbolRoles = int32(scip.SymbolRole_Definition)
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "pkg/foo/foo.go",
		Symbols:      []*scip.SymbolInformation{{Symbol: monikerV1, DisplayName: "Bar"}},
		// Deliberately out of order: the parser sorts, callers must not have to.
		Occurrences: []*scip.Occurrence{occAt(20, 8, 3), def, occAt(20, 2, 3)},
	}}}

	files, names, err := ParseOccurrences(t.Context(), marshalIndex(t, idx), "", "gomod example.com/foo Bar#")
	require.NoError(t, err)
	assert.Equal(t, []string{"Bar"}, names, "the name a rename would replace")
	require.Len(t, files, 1)
	occs := files[0].Occurrences
	require.Len(t, occs, 3)

	assert.Equal(t, types.SymbolOccurrence{Line: 11, Column: 6, EndLine: 11, EndColumn: 9, Definition: true}, occs[0],
		"SCIP 0-based (10,5)-(10,8) becomes 1-based (11,6)-(11,9), flagged as the definition")
	assert.Equal(t, 21, occs[1].Line)
	assert.Equal(t, 3, occs[1].Column, "the (20,2) occurrence sorts before (20,8)")
	assert.Equal(t, 9, occs[2].Column)
	assert.False(t, occs[1].Definition, "a plain reference is not a definition")
}

// TestParseOccurrencesSkipsOtherSymbolsAndOutsideDocuments keeps the read scoped. A
// document outside the workspace is dropped for the same reason ParseIndex drops it - it
// describes code this workspace does not own - and an occurrence of a different symbol
// must not ride along into a rewrite plan.
func TestParseOccurrencesSkipsOtherSymbolsAndOutsideDocuments(t *testing.T) {
	other := occAt(0, 0, 3)
	other.Symbol = "scip-go gomod example.com/foo v1 Other#"
	local := occAt(1, 0, 3)
	local.Symbol = "local 3"
	idx := &scip.Index{Documents: []*scip.Document{
		{RelativePath: "pkg/foo/foo.go", Occurrences: []*scip.Occurrence{occAt(2, 0, 3), other, local}},
		{RelativePath: "../../../elsewhere/dep.go", Occurrences: []*scip.Occurrence{occAt(3, 0, 3)}},
	}}

	files, _, err := ParseOccurrences(t.Context(), marshalIndex(t, idx), "", "gomod example.com/foo Bar#")
	require.NoError(t, err)
	require.Len(t, files, 1, "the out-of-workspace document is dropped entirely")
	assert.Equal(t, "pkg/foo/foo.go", files[0].File)
	require.Len(t, files[0].Occurrences, 1, "only the requested symbol, and never a local one")
	assert.Equal(t, 3, files[0].Occurrences[0].Line)
}

// TestParseOccurrencesRebasesOntoTheProject covers a nested project: SCIP paths are
// relative to where the indexer ran, so an un-rebased path would name a file at the
// workspace root that does not exist, and every edit would miss.
func TestParseOccurrencesRebasesOntoTheProject(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "vm/value.go",
		Occurrences:  []*scip.Occurrence{occAt(0, 0, 3)},
	}}}
	files, _, err := ParseOccurrences(t.Context(), marshalIndex(t, idx), "libs/gopherbuzz", "gomod example.com/foo Bar#")
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, "libs/gopherbuzz/vm/value.go", files[0].File)
}

// TestVerifyConfirmsRangesAgainstTheTree is the happy path: the range holds exactly the
// name, so the site is safe to edit and carries the text as evidence.
func TestVerifyConfirmsRangesAgainstTheTree(t *testing.T) {
	files := []types.SymbolOccurrenceFile{{
		File: "a.go",
		Occurrences: []types.SymbolOccurrence{
			{Line: 1, Column: 6, EndLine: 1, EndColumn: 9},
			{Line: 2, Column: 1, EndLine: 2, EndColumn: 4},
		},
	}}
	require.NoError(t, VerifyOccurrences(t.Context(), files, []string{"Bar"}, readerFor(map[string]string{"a.go": "func Bar() {}\nBar()\n"})))
	got := files

	for _, occ := range got[0].Occurrences {
		assert.Equal(t, types.SymbolOccurrenceVerified, occ.Status)
		assert.Equal(t, "Bar", occ.Text, "the text read back is the evidence behind the status")
	}
}

// TestVerifyRejectsAStaleIndex is the test this whole feature turns on. The index says
// the symbol sits at 1:6, but the file has been edited since and now holds something else
// there. Verification must refuse the site. If this ever passes as verified, magus would
// be handing an agent a confident instruction to corrupt a file.
func TestVerifyRejectsAStaleIndex(t *testing.T) {
	files := []types.SymbolOccurrenceFile{{
		File:        "a.go",
		Occurrences: []types.SymbolOccurrence{{Line: 1, Column: 6, EndLine: 1, EndColumn: 9}},
	}}
	// The declaration picked up a receiver, shifting every column on the line.
	require.NoError(t, VerifyOccurrences(t.Context(), files, []string{"Bar"}, readerFor(map[string]string{"a.go": "func (r T) Bar() {}\n"})))
	got := files

	occ := got[0].Occurrences[0]
	assert.Equal(t, types.SymbolOccurrenceMismatch, occ.Status, "a shifted range must never read as editable")
	assert.Equal(t, "(r ", occ.Text, "and it reports what is really there, which is what shows the index is stale")
}

// TestVerifyReportsUnreadableSites separates "the range is wrong" from "magus could not
// look", because the fixes differ: a mismatch wants a closer look at the line, an
// unreadable file wants a re-index. Both are past-the-end cases a shortened file produces.
func TestVerifyReportsUnreadableSites(t *testing.T) {
	for _, tc := range []struct {
		name  string
		occ   types.SymbolOccurrence
		files map[string]string
	}{
		{"file is gone", types.SymbolOccurrence{Line: 1, Column: 1, EndLine: 1, EndColumn: 4}, map[string]string{}},
		{"line past the end", types.SymbolOccurrence{Line: 9, Column: 1, EndLine: 9, EndColumn: 4}, map[string]string{"a.go": "Bar\n"}},
		{"column past the end", types.SymbolOccurrence{Line: 1, Column: 1, EndLine: 1, EndColumn: 99}, map[string]string{"a.go": "Bar\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := []types.SymbolOccurrenceFile{{File: "a.go", Occurrences: []types.SymbolOccurrence{tc.occ}}}
			require.NoError(t, VerifyOccurrences(t.Context(), files, []string{"Bar"}, readerFor(tc.files)))
			got := files
			assert.Equal(t, types.SymbolOccurrenceUnreadable, got[0].Occurrences[0].Status)
		})
	}
}

// TestVerifyRejectsAMultiLineRange covers the UTF-16 hazard by its observable shape. magus
// reads SCIP positions as byte offsets; a document encoded in UTF-16 offsets with
// non-ASCII text before the symbol resolves somewhere else entirely, and can span a line
// break. An identifier never does, so the name comparison catches it - the point being
// that a wrong encoding assumption degrades to a refusal, not to a wrong edit.
func TestVerifyRejectsAMultiLineRange(t *testing.T) {
	files := []types.SymbolOccurrenceFile{{
		File:        "a.go",
		Occurrences: []types.SymbolOccurrence{{Line: 1, Column: 1, EndLine: 2, EndColumn: 2}},
	}}
	require.NoError(t, VerifyOccurrences(t.Context(), files, []string{"Bar"}, readerFor(map[string]string{"a.go": "Bar\nBar\n"})))
	got := files
	assert.Equal(t, types.SymbolOccurrenceMismatch, got[0].Occurrences[0].Status)
	assert.Equal(t, "Bar\nB", got[0].Occurrences[0].Text)
}

// TestVerifyWithNoNamesVerifiesNothing pins the conservative default. An index that names
// the symbol nowhere leaves nothing to compare against, and "no expectation" must not
// collapse into "everything matches" - that would verify every site by vacuum, which is
// the exact failure this design exists to prevent.
func TestVerifyWithNoNamesVerifiesNothing(t *testing.T) {
	files := []types.SymbolOccurrenceFile{{
		File:        "a.go",
		Occurrences: []types.SymbolOccurrence{{Line: 1, Column: 1, EndLine: 1, EndColumn: 4}},
	}}
	require.NoError(t, VerifyOccurrences(t.Context(), files, nil, readerFor(map[string]string{"a.go": "Bar\n"})))
	got := files
	assert.Equal(t, types.SymbolOccurrenceMismatch, got[0].Occurrences[0].Status)
}

// TestVerifyReadsEachFileOnce guards the cost. A hot symbol is exactly the case that
// motivates the uncapped list, and re-reading per occurrence would make verifying it
// quadratic in the file's own occurrence count.
func TestVerifyReadsEachFileOnce(t *testing.T) {
	var reads int
	occs := make([]types.SymbolOccurrence, 50)
	for i := range occs {
		occs[i] = types.SymbolOccurrence{Line: 1, Column: 1, EndLine: 1, EndColumn: 4}
	}
	files := []types.SymbolOccurrenceFile{{File: "a.go", Occurrences: occs}}

	require.NoError(t, VerifyOccurrences(t.Context(), files, []string{"Bar"}, func(p string) ([]byte, error) {
		reads++
		return []byte("Bar\n"), nil
	}))
	assert.Equal(t, 1, reads, "one read for %d occurrences in one file", len(occs))
}

// TestVerifyMarksAFileStaleOnAnyBadSite covers the blind spot the per-site status cannot:
// a file whose ranges have moved may also be MISSING occurrences added since it was
// indexed, and a caller that only skipped the bad sites would silently ship an incomplete
// rewrite. One failure condemns the file, not just the site.
func TestVerifyMarksAFileStaleOnAnyBadSite(t *testing.T) {
	files := []types.SymbolOccurrenceFile{{
		File: "a.go",
		Occurrences: []types.SymbolOccurrence{
			{Line: 1, Column: 1, EndLine: 1, EndColumn: 4}, // verifies
			{Line: 2, Column: 1, EndLine: 2, EndColumn: 4}, // does not
		},
	}}
	require.NoError(t, VerifyOccurrences(t.Context(), files, []string{"Bar"}, readerFor(map[string]string{"a.go": "Bar\nQux\n"})))
	got := files

	assert.Equal(t, types.SymbolOccurrenceVerified, got[0].Occurrences[0].Status)
	assert.Equal(t, types.SymbolOccurrenceMismatch, got[0].Occurrences[1].Status)
	assert.True(t, got[0].Stale, "one moved range condemns the file's whole occurrence list")
}

// TestVerifyLeavesAGoodFileUnmarked is the other half: staleness must be a real signal,
// not a flag that is always on. A file where every range still holds the name is not
// marked, so a caller can act on the common case without a standing warning.
func TestVerifyLeavesAGoodFileUnmarked(t *testing.T) {
	files := []types.SymbolOccurrenceFile{{
		File:        "a.go",
		Occurrences: []types.SymbolOccurrence{{Line: 1, Column: 1, EndLine: 1, EndColumn: 4}},
	}}
	require.NoError(t, VerifyOccurrences(t.Context(), files, []string{"Bar"}, readerFor(map[string]string{"a.go": "Bar\n"})))
	got := files
	assert.False(t, got[0].Stale)
}

// TestParseOccurrencesKeepsBothSpellingsOfAPackage covers the case that made a fresh index
// report as stale. A package's SymbolInformation display name is its full import path,
// which appears ONLY in the import statement; every call site holds the bare identifier.
// Verifying against the display name alone rejected 8025 of 8501 real sites on this repo's
// own index, so both spellings have to survive the parse - identifier first, because that
// is the one a rename targets.
func TestParseOccurrencesKeepsBothSpellingsOfAPackage(t *testing.T) {
	const pkgMoniker = "scip-go gomod github.com/stretchr/testify v1 `github.com/stretchr/testify/assert`/"
	occ := occAt(0, 8, 34)
	occ.Symbol = pkgMoniker
	call := occAt(3, 1, 6)
	call.Symbol = pkgMoniker
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "a_test.go",
		Symbols:      []*scip.SymbolInformation{{Symbol: pkgMoniker, DisplayName: "github.com/stretchr/testify/assert"}},
		Occurrences:  []*scip.Occurrence{occ, call},
	}}}

	files, names, err := ParseOccurrences(t.Context(), marshalIndex(t, idx), "", "gomod github.com/stretchr/testify `github.com/stretchr/testify/assert`/")
	require.NoError(t, err)
	require.Equal(t, []string{"assert", "github.com/stretchr/testify/assert"}, names,
		"identifier first (what a rename targets), import path second")

	require.NoError(t, VerifyOccurrences(t.Context(), files, names, readerFor(map[string]string{
		"a_test.go": "import \"github.com/stretchr/testify/assert\"\n\nfunc f() {\n assert.Equal(t, 1, 1)\n}\n",
	})))
	got := files
	require.Len(t, got[0].Occurrences, 2)
	for _, o := range got[0].Occurrences {
		assert.Equal(t, types.SymbolOccurrenceVerified, o.Status, "both spellings verify")
	}
	assert.False(t, got[0].Stale, "a fresh index must not report as stale just because a symbol is written two ways")
}

// TestVerifyRejectsAnUnrelatedSpelling is the guard on the change above: widening to a set
// must not widen to anything. A range holding neither spelling is still a mismatch.
func TestVerifyRejectsAnUnrelatedSpelling(t *testing.T) {
	files := []types.SymbolOccurrenceFile{{
		File:        "a.go",
		Occurrences: []types.SymbolOccurrence{{Line: 1, Column: 1, EndLine: 1, EndColumn: 4}},
	}}
	require.NoError(t, VerifyOccurrences(t.Context(), files, []string{"Bar", "example.com/Bar"}, readerFor(map[string]string{"a.go": "Qux\n"})))
	got := files
	assert.Equal(t, types.SymbolOccurrenceMismatch, got[0].Occurrences[0].Status)
}

// TestParseOccurrencesReadsTheDisplayNameFromAnyDocument pins the fix for a scan that sat
// below the workspace filter. A package's SymbolInformation can be recorded only in a
// dependency document, which is dropped for its occurrences - but dropping its NAME too
// costs the full-path spelling, and then every import statement in the workspace reads as
// a mismatch and condemns its file as stale. The name is a fact about the symbol, not
// about the document that happened to carry it.
func TestParseOccurrencesReadsTheDisplayNameFromAnyDocument(t *testing.T) {
	// The display name must be the ONLY source of this spelling, or the test proves
	// nothing: monikerV1's descriptor already yields "Bar", so a path-shaped DisplayName
	// is what makes the two distinguishable.
	idx := &scip.Index{Documents: []*scip.Document{
		// Out of the workspace: its occurrences are dropped, its naming is not.
		{
			RelativePath: "../../../elsewhere/dep.go",
			Symbols:      []*scip.SymbolInformation{{Symbol: monikerV1, DisplayName: "com/foo/Bar"}},
		},
		{RelativePath: "a.go", Occurrences: []*scip.Occurrence{occAt(0, 0, 3)}},
	}}

	_, names, err := ParseOccurrences(t.Context(), marshalIndex(t, idx), "", "gomod example.com/foo Bar#")
	require.NoError(t, err)
	assert.Equal(t, []string{"Bar", "com/foo/Bar"}, names,
		"the display name must survive a document the workspace filter drops, without taking first place")
}

// TestSpellingsTakeThePrimaryFromTheIdentifier guards the one slot with a contract.
// names[0] is what a caller renames, so it has to come from the symbol's own descriptor.
// An indexer that reports a path-shaped DisplayName beside a plain identifier must not be
// able to push its own spelling into that position.
func TestSpellingsTakeThePrimaryFromTheIdentifier(t *testing.T) {
	assert.Equal(t, []string{"bar", "Baz", "com/foo/Baz"}, spellings("bar", "com/foo/Baz"),
		"a plain identifier keeps first place over a path-shaped display name")
	assert.Equal(t, []string{"assert", "github.com/x/assert"}, spellings("github.com/x/assert", "github.com/x/assert"),
		"a package's identifier is its last segment, which is what call sites write")
	assert.Equal(t, []string{"Bar"}, spellings("Bar", "Bar"))
}

// TestParseOccurrencesCollapsesDuplicateSites covers an index that records one site twice -
// which SCIP permits, and which upstream ships FlattenOccurrences to handle. Duplicated,
// it is harmless in a count and destructive in an edit plan: applying back-to-front, the
// second replacement lands on bytes the first already rewrote.
func TestParseOccurrencesCollapsesDuplicateSites(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{
		{RelativePath: "a.go", Occurrences: []*scip.Occurrence{occAt(4, 2, 3)}},
		{RelativePath: "a.go", Occurrences: []*scip.Occurrence{occAt(4, 2, 3), occAt(9, 1, 3)}},
	}}

	files, _, err := ParseOccurrences(t.Context(), marshalIndex(t, idx), "", "gomod example.com/foo Bar#")
	require.NoError(t, err)
	require.Len(t, files, 1, "two Document entries for one path yield one file")
	require.Len(t, files[0].Occurrences, 2, "the repeated site collapses; the distinct one survives")
	assert.Equal(t, 5, files[0].Occurrences[0].Line)
	assert.Equal(t, 10, files[0].Occurrences[1].Line)
}

// TestParseOccurrencesSkipsAnUnreadableRange pins the deliberate omission. An occurrence
// whose range will not resolve is dropped rather than emitted at a defaulted position: a
// site reported at 1:1 looks like an ordinary editable one, and a caller would rewrite the
// first three characters of the file.
func TestParseOccurrencesSkipsAnUnreadableRange(t *testing.T) {
	bad := &scip.Occurrence{Symbol: monikerV1} // no range at all
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "a.go",
		Occurrences:  []*scip.Occurrence{bad, occAt(7, 0, 3)},
	}}}

	files, _, err := ParseOccurrences(t.Context(), marshalIndex(t, idx), "", "gomod example.com/foo Bar#")
	require.NoError(t, err)
	require.Len(t, files[0].Occurrences, 1, "only the resolvable range is reported")
	assert.Equal(t, 8, files[0].Occurrences[0].Line)
}

// TestOccurrenceReadsObserveCancellation covers both halves of the walk. A hot symbol
// spans hundreds of files and an index carries thousands of documents, so a cancelled
// caller must not have to wait either out.
func TestOccurrenceReadsObserveCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	idx := &scip.Index{Documents: []*scip.Document{{RelativePath: "a.go", Occurrences: []*scip.Occurrence{occAt(0, 0, 3)}}}}
	_, _, err := ParseOccurrences(ctx, marshalIndex(t, idx), "", "gomod example.com/foo Bar#")
	assert.ErrorIs(t, err, context.Canceled)

	files := []types.SymbolOccurrenceFile{{File: "a.go", Occurrences: []types.SymbolOccurrence{{Line: 1, Column: 1, EndLine: 1, EndColumn: 4}}}}
	var reads int
	err = VerifyOccurrences(ctx, files, []string{"Bar"}, func(string) ([]byte, error) {
		reads++
		return []byte("Bar\n"), nil
	})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, reads, "a cancelled verify reads nothing")
}
