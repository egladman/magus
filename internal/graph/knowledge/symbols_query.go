package knowledge

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/types"
)

// symbolsNamesFile is the index file a symbol query ranks from instead of decoding every
// symbol shard: each lazy node's merged identity and searchable fields, and, for every node
// id, the shards holding that node or an edge touching it. Like the routing file it is a
// pure function of the symbol shards, bound to them by the same key, and never the source
// of truth: absent or stale, a query loads every shard as it always did.
const symbolsNamesFile = "@symbols.names.json"

// symbolNames is the index file's on-disk form.
type symbolNames struct {
	ShardsKey string `json:"shards_key"`
	// Shards names the symbol shards; Touch refers to them by index.
	Shards []string `json:"shards"`
	// Nodes is every symbol shard's nodes merged in shard-name order, the order
	// MergeSymbolShards merges them in, with attrs cut to the ones ranking reads.
	Nodes []types.KnowledgeNode `json:"nodes"`
	// Touch maps a node id's symbolRefKey to the shards holding that node or an edge
	// with it as an endpoint: exactly the shards its merged content and adjacency come from.
	Touch map[string][]int `json:"touch"`
}

// rankedAttrs are the node attrs scoreNode and a match read. Any other attr reaches a
// query's output only through the neighborhood, which is built from decoded shards.
var rankedAttrs = []string{attrLanguage, attrRole, types.AttrLayer, types.AttrMarkerFamily, AttrStaleness, AttrOutrunDays}

func (s *Store) namesPath() string { return filepath.Join(s.dir, "shards", symbolsNamesFile) }

// buildSymbolNames derives the index file from the symbol shards among shards.
func buildSymbolNames(shards []Shard, key string) symbolNames {
	var syms []Shard
	for _, sh := range shards {
		if isSymbolsShard(sh.Name) {
			syms = append(syms, sh)
		}
	}
	slices.SortFunc(syms, func(a, b Shard) int { return strings.Compare(a.Name, b.Name) })
	out := symbolNames{ShardsKey: key, Touch: map[string][]int{}}
	merged := NewGraph()
	touch := map[string]map[int]bool{}
	mark := func(id string, i int) {
		k := symbolRefKey(id)
		if touch[k] == nil {
			touch[k] = map[int]bool{}
		}
		touch[k][i] = true
	}
	for i, sh := range syms {
		out.Shards = append(out.Shards, sh.Name)
		for _, n := range sh.Nodes {
			merged.AddNode(n)
			mark(n.ID, i)
		}
		for _, e := range sh.Edges {
			mark(e.Source, i)
			mark(e.Target, i)
		}
	}
	for _, n := range merged.Nodes() {
		if n.Kind != types.KindMarker {
			kept := map[string]string{}
			for _, k := range rankedAttrs {
				if v, ok := n.Attrs[k]; ok {
					kept[k] = v
				}
			}
			n.Attrs = nilIfEmpty(kept)
		}
		out.Nodes = append(out.Nodes, n)
	}
	for k, set := range touch {
		out.Touch[k] = slices.Sorted(maps.Keys(set))
	}
	return out
}

func (s *Store) writeSymbolNames(shards []Shard, key string) error {
	if key == "" {
		if err := os.Remove(s.namesPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	b, err := json.Marshal(buildSymbolNames(shards, key))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(s.dir, "shards"), 0o755); err != nil {
		return err
	}
	return file.WriteFileAtomic(s.namesPath(), b, 0o644)
}

// readSymbolNames returns the index file when it is bound to man's symbol shards, else nil.
// It may be shared with other readers through the read cache, so callers never write
// into it.
func (s *Store) readSymbolNames(man *manifest) *symbolNames {
	key := symbolShardsKey(man)
	if key == "" {
		return nil
	}
	path := s.namesPath()
	n, info, hit, err := decodeFile(path, func(b []byte) (*symbolNames, error) {
		var n symbolNames
		err := json.Unmarshal(b, &n)
		return &n, err
	})
	if err != nil {
		return nil
	}
	if !hit {
		readCache.putFile(path, info, n, s.dir, "", "")
	}
	if n.ShardsKey != key {
		return nil
	}
	return n
}

// QuerySymbols answers g.Query(input, budget) as though every symbol shard, and both
// overlays, had been merged into the default graph g, decoding only the shards the answer
// reads. It returns the answer and the graph its matches were ranked over, which holds
// every node with its searchable fields, for a caller that looks for a near miss.
//
// Ranking runs over the symbol names index, so it sees every node's merged identity without a
// single shard decoded. The neighborhood then needs real adjacency, and only for the nodes
// its walk visits; it loads the shards touching those, walks again, and repeats until a
// walk asks for nothing new. At that point every node the walk visited carries exactly the
// content and edges the full graph gives it, so the walk, and the answer, are the full
// graph's. A query that ranks on edges (a relation filter with no free text), or a store
// with no current index file, merges every shard instead. g is consumed.
func (s *Store) QuerySymbols(ctx context.Context, g *Graph, input string, budget int) (types.KnowledgeQueryOutput, *Graph, error) {
	man := s.readManifestOrNil()
	q := parseQuery(input)
	_, relationOnly := q.fields["relation"]
	// A process keeping its reads holds, or builds once, the fully merged graph with its
	// adjacency, which answers any later query sooner than a partial merge per query does.
	retained := mergedKey(g, man) != ""
	var names *symbolNames
	if !retained {
		names = s.readSymbolNames(man)
	}
	if names == nil || (relationOnly && len(q.terms) == 0) {
		if err := s.MergeSymbolShards(ctx, g); err != nil {
			return types.KnowledgeQueryOutput{}, nil, err
		}
		out := g.Query(input, budget)
		return out, g, nil
	}
	if budget <= 0 {
		budget = DefaultBudget
	}

	overlays := s.overlayShards(ctx, man)
	ranked := g.clone()
	ranked.Merge(names.Nodes, nil)
	for _, sh := range overlays {
		mergeOverlay(ranked, sh)
	}
	matches := ranked.Resolve(input, 0)
	seeds := make([]string, len(matches))
	for i, m := range matches {
		seeds[i] = m.ID
	}

	decoded := map[int]Shard{}
	want := map[int]bool{}
	need := func(ids ...string) bool {
		grew := false
		for _, id := range ids {
			for _, i := range names.Touch[symbolRefKey(id)] {
				if !want[i] {
					want[i], grew = true, true
				}
			}
		}
		return grew
	}
	need(seeds[:min(budget, len(seeds))]...)
	for {
		for i := range want {
			if _, ok := decoded[i]; ok {
				continue
			}
			sf, err := s.readVerifiedShard(ctx, man, names.Shards[i])
			if err != nil {
				return types.KnowledgeQueryOutput{}, nil, err
			}
			decoded[i] = Shard{Name: names.Shards[i], Nodes: sf.Nodes, Edges: sf.Edges}
		}
		walked := g.clone()
		for _, i := range slices.Sorted(maps.Keys(want)) {
			walked.Merge(decoded[i].Nodes, decoded[i].Edges)
		}
		for _, sh := range overlays {
			mergeOverlay(walked, sh)
		}
		sub, reached := walked.neighborhood(seeds, budget, q.fields["relation"])
		if !need(reached...) {
			out := sub.Output()
			return types.KnowledgeQueryOutput{
				Definition:    types.KnowledgeQueryDefinition,
				SchemaVersion: types.KnowledgeSchemaVersion,
				Query:         strings.TrimSpace(input),
				Budget:        budget,
				MatchCount:    len(matches),
				Matches:       matches,
				Nodes:         out.Nodes,
				Links:         out.Links,
			}, ranked, nil
		}
		if err := ctx.Err(); err != nil {
			return types.KnowledgeQueryOutput{}, nil, err
		}
	}
}

// overlayShards decodes the coverage and session overlays the manifest lists, in the order
// MergeSymbolShards merges them.
func (s *Store) overlayShards(ctx context.Context, man *manifest) []Shard {
	var out []Shard
	for _, name := range []string{coverageShardName, sessionShardName} {
		if _, ok := man.shard(name); !ok {
			continue
		}
		sf, err := s.readVerifiedShard(ctx, man, name)
		if err != nil {
			// mergeOverlayShard treats an unreadable overlay as absent, and so does this.
			s.log.DebugContext(ctx, "overlay unreadable", "shard", name, attr.Error(err))
			continue
		}
		out = append(out, Shard{Name: name, Nodes: sf.Nodes, Edges: sf.Edges})
	}
	return out
}

// clone copies g's nodes and edges into a fresh graph, root included.
func (g *Graph) clone() *Graph {
	c := NewGraph()
	maps.Copy(c.nodes, g.nodes)
	maps.Copy(c.edges, g.edges)
	c.root = g.root
	return c
}
