// Package graph holds the graph routes and services the server serves and the magus.graph.v1alpha1 wire
// mapping behind them: the GET /api/v1/graph route (a bulk subgraph document) and the
// GraphService RPCs (ranked retrieval: query, resolve, explain, path, stats). Both consume
// DOMAIN values and map them onto the versioned protobuf; the route encodes as snake_case
// protojson, wire-compatible with what the browser Graph Explorer already parses. The targets
// flavor has no proto twin, so it is written as its domain JSON directly (see the handler).
package graph

import (
	"github.com/egladman/magus/internal/graph/knowledge"
	graphv1 "github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1"
	"github.com/egladman/magus/types"
)

func nodesToProto(in []types.KnowledgeNode) []*graphv1.Node {
	out := make([]*graphv1.Node, 0, len(in))
	for _, n := range in {
		out = append(out, &graphv1.Node{
			Id:     n.ID,
			Kind:   n.Kind,
			Label:  n.Label,
			Doc:    n.Doc,
			Source: n.Source,
			Attrs:  n.Attrs,
		})
	}
	return out
}

func edgesToProto(in []types.KnowledgeEdge) []*graphv1.Edge {
	out := make([]*graphv1.Edge, 0, len(in))
	for _, e := range in {
		out = append(out, &graphv1.Edge{
			Source:     e.Source,
			Target:     e.Target,
			Relation:   string(e.Relation),
			Confidence: e.Confidence,
			Score:      e.Score,
			Provenance: e.Provenance,
			Attrs:      e.Attrs,
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
	}
	return graphv1.EdgeDirection_EDGE_DIRECTION_UNSPECIFIED
}

func edgeRefsToProto(in []types.KnowledgeEdgeRef) []*graphv1.EdgeRef {
	out := make([]*graphv1.EdgeRef, 0, len(in))
	for _, e := range in {
		out = append(out, &graphv1.EdgeRef{
			Relation:   string(e.Relation),
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

func sitesToProto(in []knowledge.ContextSite) []*graphv1.DefinitionSite {
	out := make([]*graphv1.DefinitionSite, 0, len(in))
	for _, s := range in {
		out = append(out, &graphv1.DefinitionSite{
			File:            s.File,
			StartLine:       int32(s.StartLine),
			EndLine:         int32(s.EndLine),
			Status:          string(s.Status),
			Source:          s.Source,
			SourceStartLine: int32(s.SourceStartLine),
			Symbol:          s.Symbol,
			MatchId:         s.MatchID,
		})
	}
	return out
}
