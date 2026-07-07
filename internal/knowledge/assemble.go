package knowledge

import "github.com/egladman/magus/types"

// Assembly is composition, not analysis: it maps machinery magus already owns
// (the static magusfile extraction behind DescribeGraph, the spell/module/
// diagnostic registries) onto knowledge nodes and edges. No execution, no LLM.
// Every Phase 1 edge is EXTRACTED with score 1.0.

// RegistryShardName is the logical name of the singleton shard holding
// workspace-independent, compiled-in entities (spells, ops, modules, methods,
// diagnostics). The leading "@" keeps it from colliding with any project path.
const RegistryShardName = "@registry"

// Inputs are the already-gathered describe outputs the assembler composes. The
// caller (the CLI/composition root) fetches these from the workspace so that
// internal/knowledge depends only on types - it never reaches into the registry,
// host, or spell packages itself.
type Inputs struct {
	Graph       types.TargetGraphOutput // DescribeGraph(): projects, targets, deps, charms, spell ops
	Spells      types.SpellsOutput      // DescribeSpells(): spell + op nodes
	Modules     []types.ModuleEntry     // host modules, each with Methods populated
	Diagnostics []types.DiagnosticCode  // AllDiagnosticCodes()
}

// Shard is a named, independently-fingerprinted slice of the graph: one per
// project (its magusfile-derived nodes) plus the singleton registry shard.
// Shards are authoritative on disk; the merged graph is assembled in memory.
type Shard struct {
	Name  string
	Nodes []types.KnowledgeNode
	Edges []types.KnowledgeEdge
}

// AssembleShards builds every shard from the gathered inputs: the registry shard
// plus one per project in the graph. Order is registry first, then projects in
// their DescribeGraph order.
func AssembleShards(in Inputs) []Shard {
	shards := make([]Shard, 0, len(in.Graph.Projects)+1)
	shards = append(shards, assembleRegistry(in))
	for _, p := range in.Graph.Projects {
		shards = append(shards, assembleProject(p))
	}
	return shards
}

// assembleRegistry builds the workspace-independent shard: spell/op nodes with
// their contains edges, module/method nodes with theirs, and diagnostic nodes
// carrying their doc URL.
func assembleRegistry(in Inputs) Shard {
	var s Shard
	s.Name = RegistryShardName

	for _, sp := range in.Spells.Spells {
		sID := spellID(sp.Name)
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:    sID,
			Kind:  types.KindSpell,
			Label: sp.Name,
		})
		for _, op := range sp.Targets {
			oID := opID(sp.Name, op)
			s.Nodes = append(s.Nodes, types.KnowledgeNode{
				ID:    oID,
				Kind:  types.KindOp,
				Label: op,
				Doc:   sp.TargetDocs[op],
			})
			s.Edges = append(s.Edges, extractedEdge(sID, oID, types.RelationContains, ""))
		}
	}

	for _, m := range in.Modules {
		mID := moduleID(m.Name)
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:    mID,
			Kind:  types.KindModule,
			Label: m.Name,
			Doc:   m.Doc,
		})
		for _, meth := range m.Methods {
			methID := methodID(m.Name, meth.Name)
			attrs := map[string]string{}
			if meth.Buzz != "" {
				attrs["buzz"] = meth.Buzz
			}
			s.Nodes = append(s.Nodes, types.KnowledgeNode{
				ID:    methID,
				Kind:  types.KindMethod,
				Label: m.Name + "." + meth.Name,
				Doc:   meth.Doc,
				Attrs: nilIfEmpty(attrs),
			})
			s.Edges = append(s.Edges, extractedEdge(mID, methID, types.RelationContains, ""))
		}
	}

	for _, code := range in.Diagnostics {
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:    diagnosticID(string(code)),
			Kind:  types.KindDiagnostic,
			Label: string(code),
			Attrs: map[string]string{"url": code.URL()},
		})
	}

	return s
}

// assembleProject builds one project's shard: the project node, its targets and
// contains edges, target->target dependencies (intra- and cross-project),
// target->op uses edges, charm->target references, and project->project deps.
func assembleProject(p types.TargetGraphProject) Shard {
	s := Shard{Name: p.Path}
	pID := projectID(p.Path)
	s.Nodes = append(s.Nodes, types.KnowledgeNode{
		ID:     pID,
		Kind:   types.KindProject,
		Label:  p.Path,
		Source: p.Path,
	})
	for _, dep := range p.DependsOn {
		s.Edges = append(s.Edges, extractedEdge(pID, projectID(dep), types.RelationDependsOn, p.Path))
	}

	for _, n := range p.Nodes {
		tID := targetID(p.Path, n.Name)
		s.Nodes = append(s.Nodes, types.KnowledgeNode{
			ID:     tID,
			Kind:   types.KindTarget,
			Label:  n.Name,
			Doc:    n.Doc,
			Source: p.Path,
		})
		s.Edges = append(s.Edges, extractedEdge(pID, tID, types.RelationContains, p.Path))

		for _, dep := range n.Dependencies {
			s.Edges = append(s.Edges, extractedEdge(tID, targetID(p.Path, dep), types.RelationDependsOn, p.Path))
		}
		for _, cd := range n.CrossDependencies {
			s.Edges = append(s.Edges, extractedEdge(tID, targetID(cd.Project, cd.Target), types.RelationDependsOn, p.Path))
		}
		for _, su := range n.Spells {
			for _, op := range su.Ops {
				oID := opID(su.Spell, op)
				// Emit a minimal op node too, so a target using an op the registry
				// shard did not declare (alias handle, workspace spell) never leaves
				// a dangling edge; it dedups against the registry's richer node.
				s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: oID, Kind: types.KindOp, Label: op})
				s.Edges = append(s.Edges, extractedEdge(tID, oID, types.RelationUses, p.Path))
			}
		}
		for _, c := range n.Charms {
			cID := charmID(c)
			s.Nodes = append(s.Nodes, types.KnowledgeNode{ID: cID, Kind: types.KindCharm, Label: c})
			// Direction per plan: a charm references the targets that declare it, so
			// its out-degree is its fan-out (the charm-fan-out metric, later phase).
			s.Edges = append(s.Edges, extractedEdge(cID, tID, types.RelationReferences, p.Path))
		}
	}
	return s
}

// extractedEdge builds a directly-observed edge (confidence extracted, score 1.0).
func extractedEdge(source, target, relation, provenance string) types.KnowledgeEdge {
	return types.KnowledgeEdge{
		Source:     source,
		Target:     target,
		Relation:   relation,
		Confidence: types.ConfidenceExtracted,
		Score:      1.0,
		Provenance: provenance,
	}
}

// nilIfEmpty returns m, or nil when m has no entries, so an empty Attrs map
// serializes as absent (omitempty) rather than {}.
func nilIfEmpty(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	return m
}
