// Package graph holds the graph routes and services the server serves and the magus.graph.v1alpha1 wire
// mapping behind them: the GET /api/v1/graph route (a bulk subgraph document) and the
// GraphService RPCs (ranked retrieval: query, resolve, explain, path, stats). Both consume
// DOMAIN values and map them onto the versioned protobuf; the route encodes as snake_case
// protojson, wire-compatible with what the browser Graph Explorer already parses. The targets
// flavor has no proto twin, so it is written as its domain JSON directly (see the handler).
package graph

import (
	"slices"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	graphv1 "github.com/egladman/magus/proto/gen/go/magus/graph/v1alpha1"
	"github.com/egladman/magus/types"
)

// graphToProto maps a domain KnowledgeGraphOutput onto the magus.graph.v1alpha1 wire message. The
// node-link field names already match (id/kind/label; source/target/relation), so the
// protojson of the result is byte-shape-compatible with the domain JSON the explorer used to
// receive; the extra count/flag fields are additive and ignored by the client.
func graphToProto(g types.KnowledgeGraphOutput) *graphv1.Graph {
	return &graphv1.Graph{
		Definition: g.Definition,
		ApiVersion: string(g.APIVersion),
		Directed:   g.Directed,
		Multigraph: g.Multigraph,
		NodeCount:  int32(g.NodeCount),
		EdgeCount:  int32(g.EdgeCount),
		SourceBase: g.SourceBaseURL,
		Nodes:      nodesToProto(g.Nodes),
		Links:      edgesToProto(g.Links),
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

// chainsToProto walks each chain from its seed, so every depends_on edge is crossed against
// its own direction: the dependent is the edge's source.
func chainsToProto(in []types.AffectedPath) []*graphv1.AffectedChain {
	out := make([]*graphv1.AffectedChain, 0, len(in))
	for _, p := range in {
		c := &graphv1.AffectedChain{Seed: projectID(p.Seed)}
		for i := 0; i+1 < len(p.Chain); i++ {
			c.Steps = append(c.Steps, &graphv1.PathStep{
				From: projectID(p.Chain[i]), To: projectID(p.Chain[i+1]),
				Relation: string(types.RelationDependsOn), Forward: false,
			})
		}
		out = append(out, c)
	}
	return out
}

// seedsToProto charges each changed path to the seed the affected set put it under, so every
// project named here is one in ids.
func seedsToProto(r *types.AffectedResult, files []types.FileEntry) []*graphv1.AffectedSeed {
	owner := map[string]string{}
	for seed, paths := range r.FilesBySeed {
		for _, p := range paths {
			owner[p] = seed
		}
	}
	undeclared := map[string]bool{}
	for _, paths := range r.UndeclaredBySeed {
		for _, p := range paths {
			undeclared[p] = true
		}
	}
	role := make(map[string]string, len(files))
	for _, f := range files {
		role[f.Path] = f.Role
	}
	changed := slices.Sorted(slices.Values(r.Changed))
	out := make([]*graphv1.AffectedSeed, 0, len(changed))
	for _, p := range changed {
		s := &graphv1.AffectedSeed{Path: p, Role: role[p], Undeclared: undeclared[p]}
		if seed, ok := owner[p]; ok {
			s.Project = projectID(seed)
		}
		out = append(out, s)
	}
	return out
}

// planToProto flattens the shards to their units and keeps each unit's first place, since
// stage one may replay a unit stage zero computed.
func planToProto(target, base string, plan types.ShardPlan) *graphv1.AffectedPlan {
	args := []string{target}
	if base != "" {
		args = append(args, "--base", base)
	}
	out := &graphv1.AffectedPlan{
		Target:      target,
		Source:      plan.Source,
		UnboundedBy: plan.UnboundedBy,
		Command:     hint.Affected.WithAs(hint.DefaultBinaryName, args...),
		Unreached:   unitsToProto(plan.Unreached),
	}
	seen := map[types.Unit]bool{}
	for _, s := range slices.Concat(plan.StageZero, plan.Shards) {
		for _, u := range s.Units {
			if !seen[u] {
				seen[u] = true
				out.Units = append(out.Units, unitToProto(u))
			}
		}
	}
	return out
}

func unitsToProto(in []types.Unit) []*graphv1.PlanUnit {
	out := make([]*graphv1.PlanUnit, 0, len(in))
	for _, u := range in {
		out = append(out, unitToProto(u))
	}
	return out
}

func unitToProto(u types.Unit) *graphv1.PlanUnit {
	return &graphv1.PlanUnit{Project: projectID(u.Project), Target: u.Target}
}
