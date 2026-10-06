package knowledge

import (
	"strings"

	"github.com/egladman/magus/types"
)

// buzzLanguage is the language a Buzz symbol index stamps on its symbols.
const buzzLanguage = "buzz"

// supersedeBuzzFunctions retires the @buzz shard's function nodes for every file a merged
// Buzz symbol index covers, so a file's functions are one symbol node each rather than a
// symbol node and a function node that say the same thing. It runs where the symbol shards
// merge, never in the @buzz shard itself: the domain graph is committed, and must not vary
// with whether this machine has built a Buzz index.
//
// A file is covered when a merged Buzz symbol is defined in it. For such a file the
// function nodes go, with their contains and calls edges (the index carries defines and
// calls of its own); the file node, its import edges and its rationale nodes stay. An edge
// that pointed at a retired function moves to the function's symbol when the relation
// allows that shape, which binds each rationale to its enclosing function's symbol, and to
// the file otherwise. A file no merged Buzz index covers keeps every function node.
func (g *Graph) supersedeBuzzFunctions() {
	symbolOf := buzzFunctionSymbols(g.nodes)
	if len(symbolOf) == 0 {
		return
	}
	// retired maps each superseded function node to the node its edges move to.
	retired := map[string]string{}
	for id, n := range g.nodes {
		if n.Kind != types.KindFunction {
			continue
		}
		rel, name, ok := parseFunctionID(id)
		if !ok {
			continue
		}
		byName, covered := symbolOf[rel]
		if !covered {
			continue
		}
		retired[id] = byName[name]
		if retired[id] == "" {
			retired[id] = fileID(rel)
		}
	}
	if len(retired) == 0 {
		return
	}
	g.own()
	var moved []types.KnowledgeEdge
	for k, e := range g.edges {
		_, fromRetired := retired[e.Source]
		to, toRetired := retired[e.Target]
		if !fromRetired && !toRetired {
			continue
		}
		delete(g.edges, k)
		if fromRetired || e.Relation == types.RelationContains || e.Relation == types.RelationCalls {
			continue
		}
		source, ok := g.nodes[e.Source]
		if !ok {
			continue
		}
		if target, ok := g.nodes[to]; !ok || !types.KnowledgeRelationAllows(e.Relation, source.Kind, target.Kind) {
			rel, _, _ := parseFunctionID(e.Target)
			to = fileID(rel)
		}
		e.Target = to
		moved = append(moved, e)
	}
	for id := range retired {
		delete(g.nodes, id)
	}
	g.out, g.in, g.search = nil, nil, nil
	for _, e := range moved {
		g.AddEdge(e)
	}
}

// buzzFunctionSymbols maps each file a Buzz symbol is defined in to its top-level
// functions' symbol IDs by name. A file with Buzz symbols but no top-level function still
// appears, with an empty map: it is covered all the same.
func buzzFunctionSymbols(nodes map[string]types.KnowledgeNode) map[string]map[string]string {
	out := map[string]map[string]string{}
	for id, n := range nodes {
		if n.Kind != types.KindSymbol || n.Attrs[attrLanguage] != buzzLanguage {
			continue
		}
		rel, _, ok := strings.Cut(n.Source, ":")
		if !ok || rel == "" {
			continue
		}
		if out[rel] == nil {
			out[rel] = map[string]string{}
		}
		// A top-level fun's descriptor is the file's namespace and then name(): scip-buzz
		// writes `hack/x.buzz`/parse(). for parse in hack/x.buzz. A method or a parameter
		// carries more descriptors, so the suffix tells them apart from the function.
		if suffix := "`" + rel + "`/" + n.Label + "()."; strings.HasSuffix(id, suffix) {
			out[rel][n.Label] = id
		}
	}
	return out
}

// supersedingSymbol resolves the ID of a function node supersedeBuzzFunctions retired to
// the Buzz symbol that replaced it, so a function ID a reader kept from an earlier answer
// still reaches the function. ok is false for any other ref, a function node still in g
// included.
func (g *Graph) supersedingSymbol(ref string) (string, bool) {
	if _, present := g.node(ref); present {
		return "", false
	}
	rel, name, ok := parseFunctionID(ref)
	if !ok {
		return "", false
	}
	suffix := "`" + rel + "`/" + name + "()."
	for id, n := range g.nodes {
		if n.Kind == types.KindSymbol && n.Attrs[attrLanguage] == buzzLanguage && strings.HasSuffix(id, suffix) {
			return id, true
		}
	}
	return "", false
}

// parseFunctionID splits a function node ID (see functionID) into its file and name.
func parseFunctionID(id string) (rel, name string, ok bool) {
	rest, ok := strings.CutPrefix(id, types.KindFunction+":")
	if !ok {
		return "", "", false
	}
	i := strings.LastIndex(rest, ":")
	if i <= 0 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}
