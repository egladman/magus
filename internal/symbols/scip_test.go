package symbols

import (
	"testing"

	"github.com/scip-code/scip/bindings/go/scip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// monikerV1/V2 are the same symbol (example.com/foo Bar type) at two package
// versions; ParseIndex must collapse them to one ID (version stripped).
const (
	monikerV1 = "scip-go gomod example.com/foo v1 Bar#"
	monikerV2 = "scip-go gomod example.com/foo v2 Bar#"
)

func marshalIndex(t *testing.T, idx *scip.Index) []byte {
	t.Helper()
	b, err := proto.Marshal(idx)
	require.NoError(t, err)
	return b
}

func TestParseIndexDefsRefsAndDedup(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{
		{
			RelativePath: "pkg/foo/foo.go",
			Language:     "go",
			Symbols:      []*scip.SymbolInformation{{Symbol: monikerV1, DisplayName: "Bar", Kind: scip.SymbolInformation_Type}},
			Occurrences: []*scip.Occurrence{
				{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{10, 5, 8}},
				{Symbol: "local 3", Range: []int32{0, 0, 1}}, // local -> skipped
			},
		},
		{
			RelativePath: "pkg/baz/baz.go",
			Language:     "go",
			Occurrences: []*scip.Occurrence{
				{Symbol: monikerV1, Range: []int32{4, 0, 3}}, // ref, line 5
				{Symbol: monikerV2, Range: []int32{7, 0, 3}}, // same symbol, other version -> merges; line 8
			},
		},
	}}

	syms, err := ParseIndex(marshalIndex(t, idx), "")
	require.NoError(t, err)
	require.Len(t, syms, 1, "v1 and v2 collapse to one symbol")

	s := syms[0]
	assert.Equal(t, "gomod example.com/foo Bar#", s.Key, "manager kept, version stripped")
	assert.Equal(t, "Bar", s.Label, "display name from SymbolInformation")
	assert.Equal(t, "go", s.Language)
	assert.Equal(t, "pkg/foo/foo.go:11", s.Source, "1-based definition line")
	assert.Equal(t, []string{"pkg/foo/foo.go"}, s.Defs)

	require.Len(t, s.Refs, 1, "both refs are in one file -> one per-file entry")
	assert.Equal(t, "pkg/baz/baz.go", s.Refs[0].Path)
	assert.Equal(t, 2, s.Refs[0].Count, "per-file occurrence count")
	assert.Equal(t, []int{5, 8}, s.Refs[0].Lines)
}

// TestParseIndexTypedRange guards the fix for modern indexers: they set the typed
// range oneof and NOT the deprecated packed `range` field, so reading `range` alone
// would report line 0 everywhere. SourceRange must resolve the typed form.
func TestParseIndexTypedRange(t *testing.T) {
	defRange := scip.Range{Start: scip.Position{Line: 10, Character: 5}, End: scip.Position{Line: 10, Character: 8}}
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "pkg/foo/foo.go",
		Occurrences: []*scip.Occurrence{
			{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), TypedRange: defRange.AsTypedRange()},
		},
	}}}
	syms, err := ParseIndex(marshalIndex(t, idx), "")
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, "pkg/foo/foo.go:11", syms[0].Source, "typed range resolved to the 1-based line, not 0")
}

func TestParseIndexSkipsLocalAndUnparseable(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "a.go",
		Occurrences: []*scip.Occurrence{
			{Symbol: "local 1", Range: []int32{0, 0, 1}},
			{Symbol: "", Range: []int32{1, 0, 1}},
			{Symbol: "not a valid moniker", Range: []int32{2, 0, 1}},
		},
	}}}
	syms, err := ParseIndex(marshalIndex(t, idx), "")
	require.NoError(t, err)
	assert.Empty(t, syms, "local, empty, and unparseable monikers all skipped")
}

func TestParseIndexRefLineCap(t *testing.T) {
	occs := make([]*scip.Occurrence, 0, MaxRefLines+5)
	for i := 0; i < MaxRefLines+5; i++ {
		occs = append(occs, &scip.Occurrence{Symbol: monikerV1, Range: []int32{int32(i), 0, 1}})
	}
	idx := &scip.Index{Documents: []*scip.Document{{RelativePath: "big.go", Occurrences: occs}}}

	syms, err := ParseIndex(marshalIndex(t, idx), "")
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, MaxRefLines+5, syms[0].Refs[0].Count, "count is exact")
	assert.Len(t, syms[0].Refs[0].Lines, MaxRefLines, "lines are capped")
}

func TestParseIndexBadBytes(t *testing.T) {
	_, err := ParseIndex([]byte("not a protobuf"), "")
	assert.Error(t, err)
}

// TestParseIndexRebasesProjectPaths: a nested project's index emits paths relative to
// its own root; ParseIndex joins them onto the project path so they are workspace-
// relative and land on the same file nodes the rest of the graph uses.
func TestParseIndexRebasesProjectPaths(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{{
		RelativePath: "compiler.go", // indexer-relative, project is gopherbuzz
		Language:     "go",
		Occurrences: []*scip.Occurrence{
			{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 0, 3}},
			{Symbol: monikerV1, Range: []int32{4, 0, 3}, EnclosingRange: nil},
		},
	}}}
	syms, err := ParseIndex(marshalIndex(t, idx), "gopherbuzz")
	require.NoError(t, err)
	require.Len(t, syms, 1)
	assert.Equal(t, []string{"gopherbuzz/compiler.go"}, syms[0].Defs, "def path rebased under the project")
	assert.Equal(t, "gopherbuzz/compiler.go:1", syms[0].Source, "source path rebased under the project")
}

func TestParseMonikerStripsVersion(t *testing.T) {
	id1, label, ok := parseMoniker(monikerV1)
	require.True(t, ok)
	id2, _, ok := parseMoniker(monikerV2)
	require.True(t, ok)
	assert.Equal(t, id1, id2, "the two versions share one ID")
	assert.Equal(t, "Bar", label)

	_, _, ok = parseMoniker("local 5")
	assert.False(t, ok)
}

// TestWorkspacePath pins the containment rule on indexer output.
//
// scip-go records occurrences in the packages it resolved, so a document path can point
// into the module or build cache. Those paths flowed into the knowledge graph as real
// nodes: a committed graph carried 93 of them, bottoming out in one developer's
// ~/Library/Caches/go-build shards.
func TestWorkspacePath(t *testing.T) {
	tests := []struct {
		name    string
		project string
		rel     string
		want    string
		wantOK  bool
	}{
		{name: "root project path", project: ".", rel: "pkg/foo.go", want: "pkg/foo.go", wantOK: true},
		{name: "empty project path", project: "", rel: "./pkg/foo.go", want: "pkg/foo.go", wantOK: true},
		{name: "nested project rebases", project: "libs/gopherbuzz", rel: "pool.go", want: "libs/gopherbuzz/pool.go", wantOK: true},
		{name: "nested project cleans", project: "libs/gopherbuzz", rel: "./sub/../pool.go", want: "libs/gopherbuzz/pool.go", wantOK: true},

		{name: "root project escape", project: ".", rel: "../../../../../Library/Caches/go-build/01/x", wantOK: false},
		{name: "bare parent", project: ".", rel: "..", wantOK: false},
		{name: "absolute path", project: ".", rel: "/Users/someone/go/pkg/mod/x.go", wantOK: false},
		{name: "empty", project: ".", rel: "", wantOK: false},
		{name: "dot", project: ".", rel: ".", wantOK: false},

		// The ".." count here is WITHIN the project's depth, which is what makes it the
		// interesting case: validating after path.Join cancels it against the project
		// path and yields "internal/interp/x.go" with nothing left to detect, silently
		// re-attributing a document to a project that never owned it. A fixture that
		// overshoots the depth (four ".." here) is rejected either way and proves
		// nothing.
		{name: "nested project escape within its own depth", project: "libs/gopherbuzz", rel: "../../internal/interp/x.go", wantOK: false},
		{name: "nested project escape past its depth", project: "libs/gopherbuzz", rel: "../../../../escape.go", wantOK: false},
		{name: "nested project escape by one", project: "libs/gopherbuzz", rel: "../../../escape.go", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := workspacePath(tt.project, tt.rel)
			assert.Equal(t, tt.wantOK, ok)
			if !tt.wantOK {
				assert.Empty(t, got, "a refused path returns no path, so a caller ignoring ok cannot use one")
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestParseIndexSkipsEscapingDocuments checks the whole ingest, not just the helper: a
// dependency document the indexer resolved outside the workspace contributes nothing,
// while the in-workspace document beside it is unaffected.
func TestParseIndexSkipsEscapingDocuments(t *testing.T) {
	idx := &scip.Index{Documents: []*scip.Document{
		{
			RelativePath: "../../../../../Library/Caches/go-build/01/dep.go",
			Language:     "go",
			Occurrences: []*scip.Occurrence{
				{Symbol: monikerV2, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{0, 0, 3}},
			},
		},
		{
			RelativePath: "pkg/foo.go",
			Language:     "go",
			Occurrences: []*scip.Occurrence{
				{Symbol: monikerV1, SymbolRoles: int32(scip.SymbolRole_Definition), Range: []int32{7, 0, 3}},
			},
		},
	}}

	syms, err := ParseIndex(marshalIndex(t, idx), ".")
	require.NoError(t, err)
	require.Len(t, syms, 1, "only the in-workspace document contributes")
	assert.Equal(t, []string{"pkg/foo.go"}, syms[0].Defs)
	for _, s := range syms {
		assert.NotContains(t, s.Source, "..", "no symbol sources outside the workspace")
		for _, d := range s.Defs {
			assert.NotContains(t, d, "..")
		}
		for _, r := range s.Refs {
			assert.NotContains(t, r.Path, "..")
		}
	}
}
