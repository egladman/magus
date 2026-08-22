// Package graph holds the graph surfaces the daemon serves and the magus.graph.v1alpha1 wire
// mapping behind them: the GET /api/v1/graph route (a bulk subgraph document) and the
// GraphService RPCs (ranked retrieval - query, resolve, explain, path, stats). Both consume
// DOMAIN values and map them onto the versioned protobuf; the route encodes as snake_case
// protojson, wire-compatible with what the browser Graph Explorer already parses. The targets
// flavor has no proto twin, so it is written as its domain JSON directly (see the handler).
package graph

import (
	graphv1 "github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1"
	"github.com/egladman/magus/types"
)

// graphToProto maps a domain KnowledgeGraphOutput onto the magus.graph.v1alpha1 wire message. The
// node-link field names already match (id/kind/label; source/target/relation), so the
// protojson of the result is byte-shape-compatible with the domain JSON the explorer used to
// receive; the extra count/flag fields are additive and ignored by the client.
func graphToProto(g types.KnowledgeGraphOutput) *graphv1.Graph {
	return &graphv1.Graph{
		Definition:    g.Definition,
		SchemaVersion: int32(g.SchemaVersion),
		Directed:      g.Directed,
		Multigraph:    g.Multigraph,
		NodeCount:     int32(g.NodeCount),
		EdgeCount:     int32(g.EdgeCount),
		SourceBase:    g.SourceBaseURL,
		Nodes:         nodesToProto(g.Nodes),
		Links:         edgesToProto(g.Links),
	}
}

func nodeToProto(n types.KnowledgeNode) *graphv1.Node {
	return &graphv1.Node{
		Id:     n.ID,
		Kind:   n.Kind,
		Label:  n.Label,
		Doc:    n.Doc,
		Source: n.Source,
		Attrs:  n.Attrs,
	}
}

func nodesToProto(in []types.KnowledgeNode) []*graphv1.Node {
	out := make([]*graphv1.Node, 0, len(in))
	for _, n := range in {
		out = append(out, nodeToProto(n))
	}
	return out
}

func edgesToProto(in []types.KnowledgeEdge) []*graphv1.Edge {
	out := make([]*graphv1.Edge, 0, len(in))
	for _, e := range in {
		out = append(out, &graphv1.Edge{
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

func matchesToProto(in []types.KnowledgeMatch) []*graphv1.Match {
	out := make([]*graphv1.Match, 0, len(in))
	for _, m := range in {
		out = append(out, &graphv1.Match{
			Id:         m.ID,
			Kind:       m.Kind,
			Label:      m.Label,
			Score:      int32(m.Score),
			Staleness:  m.Staleness,
			OutrunDays: int32(m.OutrunDays),
		})
	}
	return out
}

// directionToProto maps the serialized domain direction onto the enum. An unrecognized value
// becomes UNSPECIFIED rather than silently reading as OUT, which would invert an edge.
func directionToProto(d types.EdgeDirection) graphv1.EdgeDirection {
	switch d {
	case types.EdgeOut:
		return graphv1.EdgeDirection_EDGE_DIRECTION_OUT
	case types.EdgeIn:
		return graphv1.EdgeDirection_EDGE_DIRECTION_IN
	default:
		return graphv1.EdgeDirection_EDGE_DIRECTION_UNSPECIFIED
	}
}

func edgeRefsToProto(in []types.KnowledgeEdgeRef) []*graphv1.EdgeRef {
	out := make([]*graphv1.EdgeRef, 0, len(in))
	for _, e := range in {
		out = append(out, &graphv1.EdgeRef{
			Relation:   e.Relation,
			Direction:  directionToProto(e.Direction),
			Other:      e.Other,
			OtherKind:  e.OtherKind,
			OtherLabel: e.OtherLabel,
			Provenance: e.Provenance,
		})
	}
	return out
}

// answerToProto flattens each gap's ProjectRef to the two fields that go on the wire. Dir is
// deliberately dropped: it is an absolute host path, and every consumer of this reads
// workspace-relative data.
func answerToProto(a types.KnowledgeAnswer) *graphv1.Answer {
	out := &graphv1.Answer{
		Verdict: string(a.Verdict),
		Reason:  string(a.Reason),
		Gaps:    make([]*graphv1.SymbolGap, 0, len(a.Gaps)),
	}
	for _, g := range a.Gaps {
		out.Gaps = append(out.Gaps, &graphv1.SymbolGap{
			ProjectPath: g.Project.Path,
			ProjectName: g.Project.Name,
			State:       string(g.State),
			Detail:      g.Detail,
		})
	}
	return out
}

func statsToProto(s types.KnowledgeStats) *graphv1.GraphStats {
	out := &graphv1.GraphStats{
		NodeCount:            int32(s.NodeCount),
		EdgeCount:            int32(s.EdgeCount),
		Gods:                 make([]*graphv1.GodNode, 0, len(s.Gods)),
		Orphans:              make([]*graphv1.Orphan, 0, len(s.Orphans)),
		Coverage:             make([]*graphv1.DocCoverage, 0, len(s.Coverage)),
		IsolatedCount:        int32(s.IsolatedCount),
		ComponentCount:       int32(s.ComponentCount),
		LargestComponentSize: int32(s.LargestComponentSize),
	}
	for _, g := range s.Gods {
		out.Gods = append(out.Gods, &graphv1.GodNode{
			Id: g.ID, Kind: g.Kind, Label: g.Label,
			Degree: int32(g.Degree), In: int32(g.In), Out: int32(g.Out),
		})
	}
	for _, o := range s.Orphans {
		out.Orphans = append(out.Orphans, &graphv1.Orphan{
			Id: o.ID, Kind: o.Kind, Label: o.Label, Reason: o.Reason,
		})
	}
	for _, c := range s.Coverage {
		out.Coverage = append(out.Coverage, &graphv1.DocCoverage{
			Kind: c.Kind, Total: int32(c.Total), Documented: int32(c.Documented),
			Percent: int32(c.Percent), Undocumented: c.Undocumented,
		})
	}
	return out
}
