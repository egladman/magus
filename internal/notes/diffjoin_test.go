package notes

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The *Node constants are deliberately NOT derivable from the SCIP key beside them. A node id
// is minted by the knowledge graph and opaque here, so a fixture that spelled one out would be
// this package asserting an id shape it is not allowed to know - and that is exactly how the
// join shipped broken: every symbol test compared a bare key against a bare key and passed,
// while production compared a bare key against a "symbol:"-prefixed node id and matched
// nothing. cmd/magus mints the real id and pins the two ends together.
const (
	cacheFile    = "internal/cache/cache.go"
	cachePut     = "m internal/cache/Store#Put()."
	cachePutNode = "node-for-put"
	cacheGet     = "m internal/cache/Store#Get()."
	cacheGetNode = "node-for-get"
	otherFile    = "internal/depgraph/graph.go"
	otherSymbol  = "m internal/depgraph/Graph#Add()."
	otherSymNode = "node-for-add"
)

func fileAnchor(note string, pos int, target string) ResolvedAnchor {
	return ResolvedAnchor{Note: note, Title: note + " title", Pos: pos,
		Anchor: Anchor{Kind: AnchorFile, Target: target}}
}

func symbolAnchor(note string, pos int, target, node, file string) ResolvedAnchor {
	return ResolvedAnchor{Note: note, Title: note + " title", Pos: pos, File: file, NodeID: node,
		Anchor: Anchor{Kind: AnchorSymbol, Target: target}}
}

func TestFileAnchorMatchesTheChangedPath(t *testing.T) {
	got := AnchorHits(
		[]ResolvedAnchor{fileAnchor("two-caches", 0, cacheFile)},
		[]string{cacheFile}, nil)

	require.Len(t, got, 1)
	assert.Equal(t, AnchorHit{
		Note: "two-caches", Title: "two-caches title", Pos: 0,
		Kind: AnchorFile, Target: cacheFile,
		Matched: cacheFile, Match: MatchFile,
	}, got[0])
}

func TestSymbolAnchorMatchesTheChangedSymbolsNodeID(t *testing.T) {
	got := AnchorHits(
		[]ResolvedAnchor{symbolAnchor("put-is-not-idempotent", 0, cachePut, cachePutNode, cacheFile)},
		nil, []string{cachePutNode})

	require.Len(t, got, 1)
	assert.Equal(t, MatchSymbol, got[0].Match)
	assert.Equal(t, cachePutNode, got[0].Matched,
		"a renderer has to say WHICH changed thing pulled the note in")
	assert.Equal(t, cachePut, got[0].Target, "the anchor still reads as the author wrote it")
}

// TestABareSymbolKeyIsNotANodeID is the regression this join was missing. A diff names its
// changed symbols in the graph's vocabulary, an anchor carries the author's SCIP key, and the
// two are not interchangeable - comparing them directly is a join that can never fire.
func TestABareSymbolKeyIsNotANodeID(t *testing.T) {
	got := AnchorHits(
		[]ResolvedAnchor{symbolAnchor("put-is-not-idempotent", 0, cachePut, cachePutNode, "")},
		nil, []string{cachePut})

	assert.Empty(t, got)
}

// TestSymbolAnchorWithNoNodeIDCannotMatch: NodeID is supplied by whoever holds the graph, and
// this package never mints one. Missing must cost the match rather than fall back to the key.
func TestSymbolAnchorWithNoNodeIDCannotMatch(t *testing.T) {
	got := AnchorHits(
		[]ResolvedAnchor{symbolAnchor("unstamped", 0, cachePut, "", "")},
		nil, []string{cachePut, cachePutNode, ""})

	assert.Empty(t, got)
}

// TestNeighborIsReportedAsTheWeakerMatch pins the distinction the whole MatchStrength field
// exists for: this note is about a symbol nobody touched, and it surfaces only because the
// edit happened to land in its file.
func TestNeighborIsReportedAsTheWeakerMatch(t *testing.T) {
	got := AnchorHits(
		[]ResolvedAnchor{symbolAnchor("get-is-hot", 0, cacheGet, cacheGetNode, cacheFile)},
		[]string{cacheFile}, []string{cachePutNode})

	require.Len(t, got, 1)
	assert.Equal(t, MatchNeighbor, got[0].Match)
	assert.Equal(t, cacheFile, got[0].Matched, "the FILE is what matched, not the symbol")
	assert.Equal(t, cacheGet, got[0].Target, "the anchor still names the symbol it is about")
}

// TestSymbolWithNoKnownFileHasNoWeakMatch: File is supplied by whoever has the graph, and this
// package never derives it. Unknown must cost the weak match rather than invent one.
func TestSymbolWithNoKnownFileHasNoWeakMatch(t *testing.T) {
	got := AnchorHits(
		[]ResolvedAnchor{symbolAnchor("unlocatable", 0, cacheGet, cacheGetNode, "")},
		[]string{cacheFile}, nil)

	assert.Empty(t, got)
}

// TestMatchStrengthWireValues pins the three spellings a consumer reads off the wire. They
// name three subjects, and deliberately not two exact tiers plus an inexact one: every match
// this join makes is exact equality, so a qualifier there would advertise a tier that has no
// implementation behind it.
func TestMatchStrengthWireValues(t *testing.T) {
	assert.Equal(t, MatchStrength("symbol"), MatchSymbol)
	assert.Equal(t, MatchStrength("file"), MatchFile)
	assert.Equal(t, MatchStrength("neighbor"), MatchNeighbor)
}

func TestDriftStatusIsCarriedThrough(t *testing.T) {
	drifted := fileAnchor("stale", 0, cacheFile)
	drifted.Status = CodeDriftedAnchor
	dangling := symbolAnchor("gone", 0, cachePut, cachePutNode, cacheFile)
	dangling.Status = CodeDanglingAnchor
	clean := fileAnchor("fresh", 0, cacheFile)
	ungraded := fileAnchor("unmeasured", 0, cacheFile)
	ungraded.Status = StatusUngraded

	got := AnchorHits([]ResolvedAnchor{drifted, dangling, clean, ungraded},
		[]string{cacheFile}, []string{cachePutNode})

	require.Len(t, got, 4)
	status := map[string]IssueCode{}
	for _, h := range got {
		status[h.Note] = h.Status
	}
	assert.Equal(t, CodeDriftedAnchor, status["stale"])
	assert.Equal(t, CodeDanglingAnchor, status["gone"])
	assert.Empty(t, status["fresh"], "a clean anchor carries no code, and must not borrow one")
	// The join grades nothing, so it cannot promote an unmeasured anchor to the clean "" a
	// renderer shows bare. That collapse is exactly what the sentinel exists to prevent.
	assert.Equal(t, StatusUngraded, status["unmeasured"])
}

func TestAnchorsTheDiffDoesNotTouchAreAbsent(t *testing.T) {
	got := AnchorHits([]ResolvedAnchor{
		fileAnchor("elsewhere", 0, otherFile),
		symbolAnchor("elsewhere-too", 0, otherSymbol, otherSymNode, otherFile),
	}, []string{cacheFile}, []string{cachePutNode})

	assert.Empty(t, got)
}

// TestUnjoinableKindsAreSkipped: a diff carries paths and symbol ids and no target identity,
// so these kinds have nothing to match against. Skipping is the documented scope, and it must
// not quietly become "matched nothing".
func TestUnjoinableKindsAreSkipped(t *testing.T) {
	for _, kind := range []AnchorKind{AnchorProject, AnchorTarget, AnchorNote} {
		t.Run(string(kind), func(t *testing.T) {
			got := AnchorHits(
				[]ResolvedAnchor{{Note: "n", Pos: 0, File: cacheFile, NodeID: cacheFile,
					Anchor: Anchor{Kind: kind, Target: cacheFile}}},
				[]string{cacheFile}, []string{cacheFile})

			assert.Empty(t, got)
		})
	}
}

// TestOneAnchorCollapsesToItsStrongestMatch: an anchor whose symbol changed AND whose file
// changed is one finding, reported at the precision that actually applies.
func TestOneAnchorCollapsesToItsStrongestMatch(t *testing.T) {
	got := AnchorHits(
		[]ResolvedAnchor{symbolAnchor("put-is-not-idempotent", 0, cachePut, cachePutNode, cacheFile)},
		[]string{cacheFile}, []string{cachePutNode})

	require.Len(t, got, 1, "the neighbor match must not be reported alongside the symbol one")
	assert.Equal(t, MatchSymbol, got[0].Match)
}

// TestARepeatedAnchorCollapses covers the degenerate note that declares one subject twice.
func TestARepeatedAnchorCollapses(t *testing.T) {
	got := AnchorHits([]ResolvedAnchor{
		fileAnchor("doubled", 3, cacheFile),
		fileAnchor("doubled", 0, cacheFile),
	}, []string{cacheFile}, nil)

	require.Len(t, got, 1)
	assert.Equal(t, 0, got[0].Pos, "a tie falls to the lower position, never to arrival order")
}

// TestTwoAnchorsOfOneNoteStayTwoHits states the boundary of the collapse rule: it is per
// anchor, not per note. A file anchor and a symbol anchor inside it are separate claims.
func TestTwoAnchorsOfOneNoteStayTwoHits(t *testing.T) {
	got := AnchorHits([]ResolvedAnchor{
		fileAnchor("two-caches", 0, cacheFile),
		symbolAnchor("two-caches", 1, cachePut, cachePutNode, cacheFile),
	}, []string{cacheFile}, []string{cachePutNode})

	require.Len(t, got, 2)
	assert.Equal(t, MatchFile, got[0].Match)
	assert.Equal(t, MatchSymbol, got[1].Match)
}

func TestOrderIsByNoteThenPositionWhateverTheInputOrder(t *testing.T) {
	in := []ResolvedAnchor{
		symbolAnchor("beta", 1, cachePut, cachePutNode, cacheFile),
		fileAnchor("alpha", 2, cacheFile),
		fileAnchor("beta", 0, cacheFile),
		symbolAnchor("alpha", 0, cacheGet, cacheGetNode, cacheFile),
	}
	want := AnchorHits(in, []string{cacheFile}, []string{cachePutNode})

	require.Len(t, want, 4)
	assert.Equal(t, []string{"alpha", "alpha", "beta", "beta"},
		[]string{want[0].Note, want[1].Note, want[2].Note, want[3].Note})
	assert.Equal(t, []int{0, 2, 0, 1},
		[]int{want[0].Pos, want[1].Pos, want[2].Pos, want[3].Pos})

	// Every rotation and the reversal: a map iterated anywhere in the join would show up here
	// as a result that moves when nothing about the workspace did.
	for i := range in {
		shuffled := slices.Concat(in[i:], in[:i])
		assert.Equal(t, want, AnchorHits(shuffled, []string{cacheFile}, []string{cachePutNode}),
			"rotation by %d", i)
	}
	reversed := slices.Clone(in)
	slices.Reverse(reversed)
	assert.Equal(t, want, AnchorHits(reversed, []string{cacheFile}, []string{cachePutNode}))
}

func TestEmptyInputsMatchNothing(t *testing.T) {
	assert.Empty(t, AnchorHits(nil, []string{cacheFile}, []string{cachePutNode}))
	assert.Empty(t, AnchorHits(
		[]ResolvedAnchor{fileAnchor("n", 0, cacheFile)}, nil, nil))

	// A blank path in the diff must not match an anchor that failed validation.
	assert.Empty(t, AnchorHits(
		[]ResolvedAnchor{fileAnchor("n", 0, "")}, []string{""}, nil))
}

// TestMatchingIsExact: the join never guesses. A prefix, a suffix, or a differently spelled
// path is a different subject, and a note surfaced against one spends trust in every later hit.
func TestMatchingIsExact(t *testing.T) {
	got := AnchorHits([]ResolvedAnchor{
		fileAnchor("prefix", 0, "internal/cache"),
		fileAnchor("suffix", 0, "cache.go"),
		fileAnchor("dotted", 0, "./"+cacheFile),
		symbolAnchor("truncated", 0, "m internal/cache/Store#", "node-for-truncated", ""),
	}, []string{cacheFile}, []string{cachePutNode})

	assert.Empty(t, got)
}
