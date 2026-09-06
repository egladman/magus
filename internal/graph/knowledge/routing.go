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
	types.KindRationale, types.KindOwner, types.KindPackage,
}

// maxAnchors caps how many high-degree anchor nodes a routing row lists.
const maxAnchors = 3

// binarySuppliedKinds are the kinds magus itself populates, from Inputs.Diagnostics
// (AllDiagnosticCodes) and Inputs.Modules (the host module registry, whose methods hang
// off it). Nothing a workspace writes adds or removes one.
//
// Their counts therefore move with the BINARY. Measured 2026-09-04: libs/textsearch's
// committed index said "70+" diagnostics while five sibling indexes said "80+", because a
// dev build carries codes a release does not, and MGS4005 then refuses to stage the
// convergence as environmental drift. Every index disagreeing with every other one is the
// permanent end state.
//
// The kinds still route: the row, the query and the anchors are all workspace-independent
// and useful. Only the size is dropped, by the renderer of the committed file.
var binarySuppliedKinds = map[string]bool{
	types.KindDiagnostic: true,
	types.KindModule:     true,
	types.KindMethod:     true,
}

// Routing derives the compact "query first" routing summary: per-kind counts with
// a few highest-degree anchor nodes, and per-project target counts with key
// targets. Degree (in + out) is the cheap "how connected / how central" proxy the
// plan calls god nodes; ties break by ID so the summary is deterministic.
//
// Two inputs are excluded because MAGUS.md is committed and drift-gated. Runtime edges,
// so the table does not rank on which diagnostics THIS machine tripped. And git history -
// the author kind and its `authored` edges - because that varies by COMMIT: a contributor
// appearing under a second identity moved the author count and rewrote a committed file
// that no source change had touched. Degree is what makes the second one subtle, since
// authored edges also decide which nodes each row lists as anchors.
//
// A third input varies the same way and is marked rather than dropped: the kinds magus
// supplies itself. See binarySuppliedKinds; their rows still route, and only the size a
// committed file would carry is withheld.
//
// `magus graph stats` keeps all of them on purpose: an interactive query wants local
// context, so its EdgeCount and god nodes differ from these. Independence from the MACHINE
// is still not claimed - the @docs/@buzz filesystem walks feed this table.
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
		for i := 0; i < len(xs) && i < maxAnchors; i++ {
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
		out.Kinds = append(out.Kinds, types.KnowledgeRoutingKind{
			Kind:       kind,
			Count:      len(xs),
			Anchors:    topLabels(xs),
			FromBinary: binarySuppliedKinds[kind],
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
