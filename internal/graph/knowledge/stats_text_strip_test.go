package knowledge

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// Which diagnostic this is does not matter; it is an edge endpoint.
var diagID = "diagnostic:" + string(types.ExecDenied)

// runtimeGraph carries both halves of what --static must drop: observed attrs beside a
// static one, and a runtime edge beside a static one.
func runtimeGraph() *Graph {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{
		ID: "target:pkg/a:build", Kind: types.KindTarget, Label: "build",
		Attrs: map[string]string{
			AttrEngine:        "buzz",
			AttrDurationP75Ms: "4200",
			AttrLastOutputRef: "out1a2b3c",
			AttrLastRunOK:     "true",
		},
	})
	g.AddNode(types.KnowledgeNode{ID: diagID, Kind: types.KindDiagnostic, Label: string(types.ExecDenied)})
	g.AddEdge(types.KnowledgeEdge{
		Source: "target:pkg/a:build", Target: diagID, Relation: types.RelationDocuments,
		Confidence: types.ConfidenceExtracted, Score: 1.0, Provenance: "magusfile.buzz",
	})
	g.AddEdge(types.KnowledgeEdge{
		Source: "target:pkg/a:build", Target: diagID, Relation: types.RelationEmits,
		Confidence: types.ConfidenceExtracted, Score: 1.0, Provenance: ProvenanceRuntime,
	})
	return g
}

// TestStripRuntimeAttrs pins what --static guarantees: the committed export carries no
// run history. Static attrs and edges survive, NodeCount holds, EdgeCount tracks.
func TestStripRuntimeAttrs(t *testing.T) {
	out := runtimeGraph().Output()
	require.Equal(t, 2, out.EdgeCount, "fixture starts with a static and a runtime edge")

	StripUnreproducible(&out)

	// Every observed attr is stripped and the static one is kept, only the static edge
	// survives with EdgeCount tracking it, and no node goes (NodeCount holds at 2).
	want := runtimeGraph().Output()
	for i := range want.Nodes {
		if want.Nodes[i].ID == "target:pkg/a:build" {
			want.Nodes[i].Attrs = map[string]string{AttrEngine: "buzz"}
		}
	}
	require.Equal(t, types.RelationDocuments, want.Links[0].Relation, "the links sort the static edge first")
	want.Links = want.Links[:1]
	want.EdgeCount = 1
	assert.Equal(t, want, out)
}

// TestStripRuntimeAttrsLeavesGraphIntact is why stripRuntimeAttrs copies instead of
// deleting in place: Output shares Attrs maps with the live graph, which in the server is
// warm and long-lived, so an in-place delete would blind every later explain.
func TestStripRuntimeAttrsLeavesGraphIntact(t *testing.T) {
	g := runtimeGraph()
	out := g.Output()
	StripUnreproducible(&out)

	live := g.Output() // re-read the live graph after the strip
	var build types.KnowledgeNode
	for _, n := range live.Nodes {
		if n.ID == "target:pkg/a:build" {
			build = n
		}
	}
	require.NotEmpty(t, build.ID)
	assert.Equal(t, "4200", build.Attrs[AttrDurationP75Ms], "the live graph keeps its observed attrs")
	assert.Equal(t, "out1a2b3c", build.Attrs[AttrLastOutputRef])
	assert.Equal(t, 2, live.EdgeCount, "the live graph keeps its runtime edge")
}

// TestStripUnreproducibleDropsGitHistory is the half that runtime stripping never covered.
//
// An observed attr varies by MACHINE; git history varies by COMMIT, which is the sharper
// problem for a checked-in export: committing anything moves the churn, so the artifact
// invalidates itself and the drift gate fires on the very commit that regenerated it. This
// repo's committed graph carried four git attrs on 204 file nodes, churn on 142 dirs, and
// 345 authored edges, all of it a function of history rather than of the tree.
func TestStripUnreproducibleDropsGitHistory(t *testing.T) {
	out := types.KnowledgeGraphOutput{
		Nodes: []types.KnowledgeNode{
			{ID: "file:a.buzz", Kind: types.KindFile, Attrs: map[string]string{
				"language": "buzz", "vcs_last_commit": "cafe", "vcs_last_modified": "2026-08-13",
				"vcs_last_author": "Ada", "vcs_commits": "7",
			}},
			{ID: "dir:pkg", Kind: types.KindDir, Attrs: map[string]string{"dir_files": "3", "dir_commits": "91", "dir_languages": "buzz"}},
			{ID: "doc:x.md", Kind: types.KindDoc, Attrs: map[string]string{"role": "readme", "staleness": "petrified", "outrun_days": "400"}},
			{ID: "author:Ada", Kind: types.KindAuthor},
		},
		Links: []types.KnowledgeEdge{
			{Source: "author:Ada", Target: "file:a.buzz", Relation: types.RelationAuthored},
			{Source: "file:a.buzz", Target: "file:b.buzz", Relation: types.RelationCoChanges},
			{Source: "doc:x.md", Target: "file:a.buzz", Relation: types.RelationDocuments},
		},
	}
	StripUnreproducible(&out)

	byID := map[string]types.KnowledgeNode{}
	for _, n := range out.Nodes {
		byID[n.ID] = n
	}
	assert.Equal(t, map[string]string{"language": "buzz"}, byID["file:a.buzz"].Attrs, "every vcs_* attr goes")
	assert.Equal(t, map[string]string{"dir_files": "3", "dir_languages": "buzz"}, byID["dir:pkg"].Attrs,
		"churn goes, but the file count and language set are functions of the tree and stay")
	assert.Equal(t, map[string]string{"role": "readme"}, byID["doc:x.md"].Attrs, "staleness is derived from two commit dates")

	// An author node exists only because history does, so it goes whole rather than
	// surviving as a bare id with everything stripped off it.
	_, ok := byID["author:Ada"]
	assert.False(t, ok)
	require.Len(t, out.Links, 1, "the authored and co_changes edges go with it")
	assert.Equal(t, types.RelationDocuments, out.Links[0].Relation)
	assert.Equal(t, len(out.Nodes), out.NodeCount)
	assert.Equal(t, len(out.Links), out.EdgeCount)
}
