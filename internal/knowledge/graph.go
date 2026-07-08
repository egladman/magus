package knowledge

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"slices"

	"github.com/egladman/magus/types"
)

// Graph is the in-memory knowledge graph: the union of every shard's nodes and
// edges, keyed for dedup and emitted in deterministic order. It is assembled at
// load time (shards are authoritative on disk; there is no continuously merged
// file). Not safe for concurrent mutation - build it on one goroutine, then read.
type Graph struct {
	nodes map[string]types.KnowledgeNode // by node ID
	edges map[edgeKey]types.KnowledgeEdge

	// Adjacency indices for traversal, built lazily on first query and assumed
	// stable thereafter (queries run after load/merge completes).
	out map[string][]types.KnowledgeEdge // by source ID
	in  map[string][]types.KnowledgeEdge // by target ID
}

// edgeKey collapses parallel edges: at most one edge per (source, target,
// relation). A second edge with the same key upgrades score/provenance if the
// newcomer is stronger, so extraction order never changes the result.
type edgeKey struct {
	source, target, relation string
}

// NewGraph returns an empty graph ready for AddNode/AddEdge/Merge.
func NewGraph() *Graph {
	return &Graph{
		nodes: map[string]types.KnowledgeNode{},
		edges: map[edgeKey]types.KnowledgeEdge{},
	}
}

// AddNode inserts a node, or upgrades an existing one with the same ID by filling
// empty fields from the newcomer. Idempotent: the same node from two shards (e.g.
// an op node the registry declares and a project references) merges cleanly, and
// the richer description wins regardless of insertion order.
func (g *Graph) AddNode(n types.KnowledgeNode) {
	n.Label = sanitize(n.Label, maxLabelLen)
	n.Doc = sanitize(n.Doc, maxDocLen)
	n.Source = sanitize(n.Source, maxSrcLen)
	// Attr values now carry file-derived text (doc frontmatter title/tags), so they
	// get the same control-char strip and length cap as the other free-form fields.
	// Sanitize into a FRESH map rather than in place: on the read paths (Output,
	// Select, Neighborhood) AddNode is fed nodes straight from g.Nodes(), whose
	// Attrs alias the live graph's own maps - mutating them there would write shared
	// state during what is logically a query. (maxLabelLen is a short cap; attrs are
	// keys and small scalars, so a label's budget is ample.)
	if len(n.Attrs) > 0 {
		clean := make(map[string]string, len(n.Attrs))
		for k, v := range n.Attrs {
			clean[k] = sanitize(v, maxLabelLen)
		}
		n.Attrs = clean
	}
	existing, ok := g.nodes[n.ID]
	if !ok {
		g.nodes[n.ID] = n
		return
	}
	if existing.Doc == "" {
		existing.Doc = n.Doc
	}
	if existing.Source == "" {
		existing.Source = n.Source
	}
	if existing.Label == "" {
		existing.Label = n.Label
	}
	if len(n.Attrs) > 0 {
		if existing.Attrs == nil {
			existing.Attrs = map[string]string{}
		}
		for k, v := range n.Attrs {
			if _, has := existing.Attrs[k]; !has {
				existing.Attrs[k] = v
			}
		}
	}
	g.nodes[n.ID] = existing
}

// AddEdge inserts a directed edge, deduplicating by (source, target, relation).
// On collision the higher-confidence edge (extracted over inferred, then higher
// score) is kept, so the merged graph is independent of shard load order.
func (g *Graph) AddEdge(e types.KnowledgeEdge) {
	e.Provenance = sanitize(e.Provenance, maxSrcLen)
	k := edgeKey{e.Source, e.Target, e.Relation}
	if prev, ok := g.edges[k]; ok && edgeStronger(prev, e) {
		return
	}
	g.edges[k] = e
	g.out, g.in = nil, nil // invalidate adjacency; rebuilt on next query
}

func (g *Graph) node(id string) (types.KnowledgeNode, bool) {
	n, ok := g.nodes[id]
	return n, ok
}

// ensureAdj builds the out/in adjacency indices from the (sorted) edge set on
// first use. Iterating Edges() keeps each adjacency list in deterministic order.
func (g *Graph) ensureAdj() {
	if g.out != nil {
		return
	}
	g.out = map[string][]types.KnowledgeEdge{}
	g.in = map[string][]types.KnowledgeEdge{}
	for _, e := range g.Edges() {
		g.out[e.Source] = append(g.out[e.Source], e)
		g.in[e.Target] = append(g.in[e.Target], e)
	}
}

// edgeStronger reports whether a should be kept over b (a is at least as strong).
func edgeStronger(a, b types.KnowledgeEdge) bool {
	if a.Confidence != b.Confidence {
		return a.Confidence == types.ConfidenceExtracted
	}
	return a.Score >= b.Score
}

// Merge folds a shard's nodes and edges into the graph.
func (g *Graph) Merge(nodes []types.KnowledgeNode, edges []types.KnowledgeEdge) {
	for _, n := range nodes {
		g.AddNode(n)
	}
	for _, e := range edges {
		g.AddEdge(e)
	}
}

// Nodes returns every node sorted by ID (stable, deterministic).
func (g *Graph) Nodes() []types.KnowledgeNode {
	out := make([]types.KnowledgeNode, 0, len(g.nodes))
	for _, n := range g.nodes {
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b types.KnowledgeNode) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

// Edges returns every edge sorted by (source, target, relation).
func (g *Graph) Edges() []types.KnowledgeEdge {
	out := make([]types.KnowledgeEdge, 0, len(g.edges))
	for _, e := range g.edges {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b types.KnowledgeEdge) int {
		if c := cmp.Compare(a.Source, b.Source); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Target, b.Target); c != 0 {
			return c
		}
		return cmp.Compare(a.Relation, b.Relation)
	})
	return out
}

// Output renders the merged graph as the node-link export. Nodes and edges are
// sorted so identical inputs produce byte-identical JSON (required for cache
// fingerprinting, golden tests, and meaningful diffs).
func (g *Graph) Output() types.KnowledgeGraphOutput {
	nodes := g.Nodes()
	edges := g.Edges()
	return types.KnowledgeGraphOutput{
		Definition:    types.KnowledgeGraphDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Directed:      true,
		Multigraph:    false,
		NodeCount:     len(nodes),
		EdgeCount:     len(edges),
		Nodes:         nodes,
		Links:         edges,
	}
}

// Fingerprint is a content hash of the graph's shape: the sorted node IDs and
// edge keys. It identifies the graph state so a stateless pagination cursor can
// detect that the graph changed underneath it (a warm-graph invalidation between
// pages) and fail loudly rather than return an incoherent slice. Deterministic
// (fed from the sorted Nodes/Edges), SHA256 to match the rest of the store.
func (g *Graph) Fingerprint() string {
	h := sha256.New()
	for _, n := range g.Nodes() {
		h.Write([]byte(n.ID))
		h.Write([]byte{0})
	}
	h.Write([]byte{'\n'})
	for _, e := range g.Edges() {
		h.Write([]byte(e.Source))
		h.Write([]byte{0})
		h.Write([]byte(e.Target))
		h.Write([]byte{0})
		h.Write([]byte(e.Relation))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil)[:8]) // 64-bit prefix: ample to spot a change
}

// --- cross-workspace union ---

// QualifierSep joins a workspace to a node ID in a global graph ("web//spell:go");
// kept a substring so fuzzy resolution still matches the bare ID.
const QualifierSep = "//"

// Qualified returns a copy of g with every node ID and edge endpoint prefixed by
// "<workspace>//", so a global graph can union many workspaces without ID
// collisions. The input graph is not modified.
func Qualified(g *Graph, workspace string) *Graph {
	q := workspace + QualifierSep
	out := NewGraph()
	for _, n := range g.Nodes() {
		n.ID = q + n.ID
		out.AddNode(n)
	}
	for _, e := range g.Edges() {
		e.Source = q + e.Source
		e.Target = q + e.Target
		out.AddEdge(e)
	}
	return out
}

// UnionInto merges src's nodes and edges into dst.
func UnionInto(dst, src *Graph) {
	dst.Merge(src.Nodes(), src.Edges())
}
