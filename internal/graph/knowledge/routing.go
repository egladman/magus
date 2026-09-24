package knowledge

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// routingKindOrder is the stable display order for the domain routing table.
// Only kinds actually present (count > 0) are emitted, so phase-4 kinds simply
// do not appear until an assembler produces them.
var routingKindOrder = []string{
	types.KindProject, types.KindTarget, types.KindSpell, types.KindOp,
	types.KindTool, types.KindCharm, types.KindModule, types.KindMethod, types.KindDiagnostic,
	types.KindDoc, types.KindDir, types.KindFile, types.KindFunction, types.KindImport,
	types.KindRationale, types.KindOwner, types.KindPackage, types.KindLink,
}

// maxAnchors caps how many high-degree anchor nodes a routing row lists.
const maxAnchors = 3

// binarySuppliedKinds are the kinds whose COUNT the binary contributes to, so a committed,
// drift-gated index carrying one disagrees with every index a different magus rendered.
//
// Measured 2026-09-04: libs/textsearch's committed index said "70+" diagnostics while five
// siblings said "80+", because a dev build carries codes a release does not, and MGS4005
// then refuses to stage the convergence as environmental drift. Every index disagreeing
// with every other one is the permanent end state.
//
// The test is whether the binary CONTRIBUTES to the count, not whether a workspace can
// add one: the three catalogs a binary carries are diagnostics, the host module registry,
// and the embedded built-in spells, which is why spell, op and tool belong here even
// though a workspace can declare its own. types.KnowledgeGraphOutput's CatalogFingerprint
// names the same three.
//
// charm is deliberately absent: types.Spell carries no charm field, so a charm node exists
// only where a magusfile writes ctx.hasCharm(...), and withholding that size would cost a
// real signal for nothing.
var binarySuppliedKinds = map[string]bool{
	types.KindDiagnostic: true,
	types.KindModule:     true,
	types.KindMethod:     true,
	types.KindSpell:      true,
	types.KindOp:         true,
	types.KindTool:       true,
}

// catalogEdge reports whether an edge joins two binary-supplied nodes: spell contains op,
// op uses tool, module contains method. Those edges describe the catalog the binary
// carries, not this workspace, so they rank nothing in a committed index.
//
// Measured 2026-09-23: a binary embedding one branch's spells rewrote every MAGUS.md on a
// branch without them. The cargo-fetch install op gave tool:cargo a seventh edge over
// tool:buf's six, and typescript's install ops lifted it past docker; the tree under the
// file had not changed. An endpoint missing from the graph counts as a workspace one, so
// a dangling edge still counts, as it did before.
func catalogEdge(g *Graph, e types.KnowledgeEdge) bool {
	src, ok := g.nodes[e.Source]
	if !ok || !binarySuppliedKinds[src.Kind] {
		return false
	}
	dst, ok := g.nodes[e.Target]
	return ok && binarySuppliedKinds[dst.Kind]
}

// Routing derives the compact "query first" routing summary: per-kind counts with
// a few highest-degree anchor nodes, and per-project target counts with key
// targets. Degree (in + out) is the cheap "how connected / how central" proxy the
// plan calls god nodes; ties break by ID so the summary is deterministic.
//
// Two inputs are excluded because MAGUS.md is committed and drift-gated. Runtime edges,
// so the table does not rank on which diagnostics THIS machine tripped. And git history
// (the author kind and its `authored` edges), because that varies by COMMIT: a contributor
// appearing under a second identity moved the author count and rewrote a committed file
// that no source change had touched. Degree is what makes the second one subtle, since
// authored edges also decide which nodes each row lists as anchors.
//
// A third input varies the same way: the catalogs magus supplies itself. Their rows still
// route, but the size is withheld (binarySuppliedKinds) and the edges inside the catalog
// do not count toward degree (catalogEdge), so a spell, op or module ranks by what the
// workspace's targets, docs and sources do with it. A node with no remaining edge is never
// an anchor, and a binary-supplied kind whose nodes all tie lists none: modules tie at one
// reference page each and tools and methods at zero, so ranking them would only report
// which id sorts first, an order the next binary's catalog rewrites.
//
// `magus graph stats` keeps all of them on purpose: an interactive query wants local
// context, so its EdgeCount and god nodes differ from these. Independence from the MACHINE
// is still not claimed: the @docs/@buzz filesystem walks feed this table.
func (g *Graph) Routing() types.KnowledgeRouting {
	type scored struct {
		label string
		deg   int
		id    string
	}
	byKind := map[string][]scored{}
	byProject := map[string][]scored{}

	// From the edge set, not the adjacency index: the index keeps runtime edges for
	// traversal. A dangling endpoint gets a deg entry the node loop never reads.
	deg := make(map[string]int, len(g.nodes))
	edgeCount := 0
	for _, e := range g.edges {
		if e.Provenance == ProvenanceRuntime || e.Relation == types.RelationAuthored {
			continue
		}
		edgeCount++
		if catalogEdge(g, e) {
			continue
		}
		deg[e.Source]++
		deg[e.Target]++
	}

	for id, n := range g.nodes {
		if n.Kind == types.KindAuthor {
			continue
		}
		s := scored{label: n.Label, deg: deg[id], id: id}
		byKind[n.Kind] = append(byKind[n.Kind], s)
		if n.Kind == types.KindTarget {
			if proj, ok := projectOfTargetID(id); ok {
				byProject[proj] = append(byProject[proj], s)
			}
		}
	}

	topLabels := func(xs []scored) []string {
		slices.SortFunc(xs, func(a, b scored) int {
			if a.deg != b.deg {
				return cmp.Compare(b.deg, a.deg)
			}
			return cmp.Compare(a.id, b.id)
		})
		out := make([]string, 0, maxAnchors)
		for i := 0; i < len(xs) && i < maxAnchors && xs[i].deg > 0; i++ {
			out = append(out, xs[i].label)
		}
		return out
	}

	out := types.KnowledgeRouting{
		SchemaVersion: types.KnowledgeSchemaVersion,
		NodeCount:     len(g.nodes),
		EdgeCount:     edgeCount,
	}
	for _, kind := range routingKindOrder {
		xs, ok := byKind[kind]
		if !ok {
			continue
		}
		anchors := topLabels(xs)
		if binarySuppliedKinds[kind] && xs[0].deg == xs[len(xs)-1].deg {
			anchors = nil
		}
		out.Kinds = append(out.Kinds, types.KnowledgeRoutingKind{
			Kind:       kind,
			Count:      len(xs),
			FromBinary: binarySuppliedKinds[kind],
			Anchors:    anchors,
		})
	}

	projects := make([]string, 0, len(byProject))
	for p := range byProject {
		projects = append(projects, p)
	}
	slices.Sort(projects)
	for _, p := range projects {
		xs := byProject[p]
		out.Projects = append(out.Projects, types.KnowledgeRoutingProject{
			Path:        p,
			TargetCount: len(xs),
			KeyTargets:  topLabels(xs),
		})
	}
	return out
}

// projectOfTargetID extracts the project path from a target node ID
// ("target:<project>:<name>" -> "<project>"). Project paths contain no colon, so
// splitting off the final segment (the target name) yields the path.
func projectOfTargetID(id string) (string, bool) {
	rest, ok := strings.CutPrefix(id, types.KindTarget+":")
	if !ok {
		return "", false
	}
	i := strings.LastIndex(rest, ":")
	if i < 0 {
		return "", false
	}
	return rest[:i], true
}
