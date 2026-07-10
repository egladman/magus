// Package graph holds the GET /api/v1/graph route handler and the magus.graph.v1 wire
// mapping behind it. It consumes DOMAIN graph values from the console service and maps the
// knowledge-graph flavors onto the versioned protobuf (encoded as snake_case protojson,
// wire-compatible with what the browser Graph Explorer already parses). The targets flavor
// has no proto twin, so it is written as its domain JSON directly (see the handler).
package graph

import (
	graphv1 "github.com/egladman/magus/proto/gen/go/magus/graph/v1"
	"github.com/egladman/magus/types"
)

// graphToProto maps a domain KnowledgeGraphOutput onto the magus.graph.v1 wire message. The
// node-link field names already match (id/kind/label; source/target/relation), so the
// protojson of the result is byte-shape-compatible with the domain JSON the explorer used to
// receive; the extra count/flag fields are additive and ignored by the client.
func graphToProto(g types.KnowledgeGraphOutput) *graphv1.Graph {
	out := &graphv1.Graph{
		Definition:    g.Definition,
		SchemaVersion: int32(g.SchemaVersion),
		Directed:      g.Directed,
		Multigraph:    g.Multigraph,
		NodeCount:     int32(g.NodeCount),
		EdgeCount:     int32(g.EdgeCount),
		SourceBase:    g.SourceBaseURL,
		Nodes:         make([]*graphv1.Node, 0, len(g.Nodes)),
		Links:         make([]*graphv1.Edge, 0, len(g.Links)),
	}
	for _, n := range g.Nodes {
		out.Nodes = append(out.Nodes, &graphv1.Node{
			Id:     n.ID,
			Kind:   n.Kind,
			Label:  n.Label,
			Doc:    n.Doc,
			Source: n.Source,
			Attrs:  n.Attrs,
		})
	}
	for _, e := range g.Links {
		out.Links = append(out.Links, &graphv1.Edge{
			Source:     e.Source,
			Target:     e.Target,
			Relation:   e.Relation,
			Confidence: e.Confidence,
			Score:      e.Score,
			Provenance: e.Provenance,
		})
	}
	return out
}
