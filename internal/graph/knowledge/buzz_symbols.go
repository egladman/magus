package knowledge

import (
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"

	"github.com/egladman/magus/types"
)

// scip-buzz's place in the SCIP symbol grammar: a workspace symbol is
// `scip-buzz buzz . . <descriptors>`, and a top-level function is the Namespace descriptor
// of its workspace-relative file and then the Method descriptor of its name.
const (
	buzzScheme  = "scip-buzz"
	buzzManager = "buzz"
)

// buzzFunction is the file and name a Buzz symbol's moniker gives a top-level function,
// and ok false for any other symbol: a method or a parameter carries more descriptors,
// another indexer another scheme.
func buzzFunction(moniker string) (rel, name string, ok bool) {
	// Every symbol in the graph is checked, and a full parse of each is the cost; the
	// scheme is the moniker's first space-separated field, so this cut is the grammar's.
	if !strings.HasPrefix(moniker, buzzScheme+" ") {
		return "", "", false
	}
	sym, err := scip.ParseSymbol(moniker)
	if err != nil || sym.Scheme != buzzScheme || sym.Package == nil || sym.Package.Manager != buzzManager {
		return "", "", false
	}
	if len(sym.Descriptors) != 2 ||
		sym.Descriptors[0].Suffix != scip.Descriptor_Namespace || sym.Descriptors[1].Suffix != scip.Descriptor_Method {
		return "", "", false
	}
	return sym.Descriptors[0].Name, sym.Descriptors[1].Name, true
}

// supersedeBuzzFunctions retires each @buzz shard function node a merged Buzz symbol index
// supersedes, so a function is one symbol node rather than a symbol node and a function
// node that say the same thing. It runs where the symbol shards merge, never in the @buzz
// shard itself: the domain graph is committed, and must not vary with whether this machine
// has built a Buzz index.
//
// A function node is superseded when the index defines a top-level function of that name in
// that file. It goes with its contains and calls edges (the index carries defines and calls
// of its own); the file node, its import edges and its rationale nodes stay. An edge that
// pointed at a retired function moves to the function's symbol when the relation allows
// that shape, which binds each rationale to its enclosing function's symbol, and to the file
// otherwise. A function the index defines no symbol for (an extern fun, which it records
// only as a forward definition, or a name it could not place) keeps its node.
func (g *Graph) supersedeBuzzFunctions() {
	symbolOf := buzzFunctionSymbols(g.nodes)
	if len(symbolOf) == 0 {
		return
	}
	// retired maps each superseded function node to the symbol its edges move to.
	retired := map[string]string{}
	for id, n := range g.nodes {
		if n.Kind != types.KindFunction {
			continue
		}
		if rel, name, ok := parseFunctionID(id); ok {
			if sym := symbolOf[rel][name]; sym != "" {
				retired[id] = sym
			}
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

// buzzFunctionSymbols maps each file to the symbol IDs of the top-level functions a Buzz
// index defines in it, by name. A symbol counts only where it is defined: one the index
// only references names nothing this graph can place.
func buzzFunctionSymbols(nodes map[string]types.KnowledgeNode) map[string]map[string]string {
	out := map[string]map[string]string{}
	for id, n := range nodes {
		if n.Kind != types.KindSymbol {
			continue
		}
		rel, name, ok := buzzFunction(n.Attrs[attrMoniker])
		if !ok {
			continue
		}
		if defined, _, _ := strings.Cut(n.Source, ":"); defined != rel {
			continue
		}
		if out[rel] == nil {
			out[rel] = map[string]string{}
		}
		out[rel][name] = id
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
	id := buzzFunctionSymbols(g.nodes)[rel][name]
	return id, id != ""
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
