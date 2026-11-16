package knowledge

import "github.com/egladman/magus/types"

// StripUnreproducible removes everything from an exported graph that is not a function of
// the source tree, leaving an artifact that regenerates byte-identically anywhere.
//
// Two kinds qualify, for the same reason and not the obvious one. Locally OBSERVED data
// (run timings, output refs, coverage, loaded agent contact) varies by machine. Git HISTORY (author nodes,
// co_changes edges, per-file churn, the dir_commits roll-up, prose staleness) varies by COMMIT, and that is
// the sharper problem for a checked-in export: committing anything changes the churn, so
// the artifact invalidates itself and the drift gate fires on the commit that just fixed
// it. The @dirs shard calls its inputs deterministic, which is true per HEAD and is not the
// property a committed file needs.
//
// Graph.Output shares node attribute maps with the live graph, so this copies rather than
// deleting in place. Interactive queries and the live graph keep all of it.
func StripUnreproducible(g *types.KnowledgeGraphOutput) {
	drop := func(key string) bool {
		return IsRuntimeAttr(key) || IsHistoryAttr(key) || IsSessionAttr(key)
	}

	nodes := g.Nodes[:0]
	for _, n := range g.Nodes {
		// An author node exists only because git history does, so it goes whole rather than
		// surviving as a bare id with every attr stripped off it.
		if n.Kind == types.KindAuthor {
			continue
		}
		kept := make(map[string]string, len(n.Attrs))
		for key, value := range n.Attrs {
			if !drop(key) {
				kept[key] = value
			}
		}
		if len(kept) == 0 {
			kept = nil
		}
		n.Attrs = kept
		nodes = append(nodes, n)
	}
	g.Nodes = nodes
	g.NodeCount = len(nodes)

	links := g.Links[:0]
	for _, link := range g.Links {
		if link.Provenance == ProvenanceRuntime || link.Relation == types.RelationAuthored || link.Relation == types.RelationCoChanges {
			continue
		}
		links = append(links, link)
	}
	g.Links = links
	g.EdgeCount = len(links)
}
