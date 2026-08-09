package main

import (
	"testing"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Which diagnostic this is does not matter; it is an edge endpoint.
var diagID = "diagnostic:" + string(types.ExecDenied)

// runtimeGraph carries both halves of what --static must drop: observed attrs beside a
// static one, and a runtime edge beside a static one.
func runtimeGraph() *knowledge.Graph {
	g := knowledge.NewGraph()
	g.AddNode(types.KnowledgeNode{
		ID: "target:pkg/a:build", Kind: types.KindTarget, Label: "build",
		Attrs: map[string]string{
			knowledge.AttrEngine:        "buzz",
			knowledge.AttrDurationP75Ms: "4200",
			knowledge.AttrLastOutputRef: "out1a2b3c",
			knowledge.AttrLastRunOK:     "true",
		},
	})
	g.AddNode(types.KnowledgeNode{ID: diagID, Kind: types.KindDiagnostic, Label: string(types.ExecDenied)})
	g.AddEdge(types.KnowledgeEdge{
		Source: "target:pkg/a:build", Target: diagID, Relation: types.RelationDocuments,
		Confidence: types.ConfidenceExtracted, Score: 1.0, Provenance: "magusfile.buzz",
	})
	g.AddEdge(types.KnowledgeEdge{
		Source: "target:pkg/a:build", Target: diagID, Relation: types.RelationEmits,
		Confidence: types.ConfidenceExtracted, Score: 1.0, Provenance: knowledge.ProvenanceRuntime,
	})
	return g
}

// TestStripRuntimeAttrs pins what --static guarantees: the committed export carries no
// run history. Static attrs and edges survive, NodeCount holds, EdgeCount tracks.
func TestStripRuntimeAttrs(t *testing.T) {
	out := runtimeGraph().Output()
	require.Equal(t, 2, out.EdgeCount, "fixture starts with a static and a runtime edge")

	stripRuntimeAttrs(&out)

	var build types.KnowledgeNode
	for _, n := range out.Nodes {
		if n.ID == "target:pkg/a:build" {
			build = n
		}
	}
	require.NotEmpty(t, build.ID, "target node survives")
	assert.Equal(t, map[string]string{knowledge.AttrEngine: "buzz"}, build.Attrs,
		"every observed attr is stripped and the static one is kept")

	require.Len(t, out.Links, 1)
	assert.Equal(t, types.RelationDocuments, out.Links[0].Relation, "only the static edge survives")
	assert.Equal(t, 1, out.EdgeCount, "EdgeCount tracks the kept links")
	assert.Equal(t, 2, out.NodeCount, "stripping removes attrs, never nodes")
}

// TestStripRuntimeAttrsLeavesGraphIntact is why stripRuntimeAttrs copies instead of
// deleting in place: Output shares Attrs maps with the live graph, which in the daemon is
// warm and long-lived, so an in-place delete would blind every later explain.
func TestStripRuntimeAttrsLeavesGraphIntact(t *testing.T) {
	g := runtimeGraph()
	out := g.Output()
	stripRuntimeAttrs(&out)

	live := g.Output() // re-read the live graph after the strip
	var build types.KnowledgeNode
	for _, n := range live.Nodes {
		if n.ID == "target:pkg/a:build" {
			build = n
		}
	}
	require.NotEmpty(t, build.ID)
	assert.Equal(t, "4200", build.Attrs[knowledge.AttrDurationP75Ms], "the live graph keeps its observed attrs")
	assert.Equal(t, "out1a2b3c", build.Attrs[knowledge.AttrLastOutputRef])
	assert.Equal(t, 2, live.EdgeCount, "the live graph keeps its runtime edge")
}
