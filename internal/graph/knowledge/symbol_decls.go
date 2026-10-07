package knowledge

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/types"
)

// SymbolDeclOptions scopes [Graph.SymbolDecls].
type SymbolDeclOptions struct {
	// Generated holds the workspace-relative paths that are generated output. Generated and
	// test code is never listed.
	Generated map[string]bool
}

// SymbolDecls lists every symbol g defines with its doc comment; g must carry its symbols.
// Nothing judges the doc: a caller reads the text and decides. Kind is empty for a symbol
// the naming index read no shape for. Declarations are ordered by source then node, so two
// runs over one graph agree byte for byte.
func (g *Graph) SymbolDecls(o SymbolDeclOptions) []types.SymbolDecl {
	x := newNamingIndex(g, ConformanceChange{Generated: o.Generated})
	var decls []types.SymbolDecl
	for id, n := range g.nodes {
		if n.Kind != types.KindSymbol {
			continue
		}
		file, _, _ := strings.Cut(n.Source, ":")
		if file == "" || isTestSource(file) || x.generated(file) {
			continue
		}
		d := types.SymbolDecl{
			Node: id, Source: n.Source, Language: n.Attrs[attrLanguage],
			Name: n.Label, Doc: n.Attrs[AttrDoc],
		}
		if nd := x.byID[id]; nd != nil {
			d.Kind, d.Owner = nd.shape.Kind, nd.owner
		}
		decls = append(decls, d)
	}
	slices.SortFunc(decls, func(a, b types.SymbolDecl) int {
		return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.Node, b.Node))
	})
	return decls
}
