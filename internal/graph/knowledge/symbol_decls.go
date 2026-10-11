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
// the naming index read no shape for. A symbol defined in several files, as a function
// written once per GOOS is, yields one declaration per file, each with that file's doc, so
// a caller judging a doc names the file that holds it. Declarations are ordered by source
// then node, so two runs over one graph agree byte for byte.
func (g *Graph) SymbolDecls(o SymbolDeclOptions) []types.SymbolDecl {
	x := newNamingIndex(g, ConformanceChange{Generated: o.Generated})
	g.ensureAdj()
	var decls []types.SymbolDecl
	for id, n := range g.nodes {
		if n.Kind != types.KindSymbol {
			continue
		}
		d := types.SymbolDecl{
			Node: id, Source: n.Source, Language: n.Attrs[attrLanguage],
			Name: n.Label, Doc: n.Attrs[AttrDoc],
		}
		if nd := x.byID[id]; nd != nil {
			d.Kind, d.Owner = nd.shape.Kind, nd.owner
		}
		defs := definitions(g.in[id])
		if len(defs) == 0 {
			defs = []types.KnowledgeSymbolDefinition{{Source: n.Source, Doc: d.Doc}}
		}
		for _, def := range defs {
			file, _, _ := strings.Cut(def.Source, ":")
			if file == "" || IsTestPath(file) || x.generated(file) {
				continue
			}
			d.Source, d.Doc = def.Source, def.Doc
			decls = append(decls, d)
		}
	}
	slices.SortFunc(decls, func(a, b types.SymbolDecl) int {
		return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.Node, b.Node))
	})
	return decls
}

// definitions reads the per-file definitions a symbol's incoming defines edges carry, empty
// for a symbol defined in one file, whose node already holds its source and doc.
func definitions(in []types.KnowledgeEdge) []types.KnowledgeSymbolDefinition {
	var defs []types.KnowledgeSymbolDefinition
	for _, e := range in {
		line := e.Attrs[types.AttrLine]
		if e.Relation != types.RelationDefines || line == "" {
			continue
		}
		file := strings.TrimPrefix(e.Source, types.KindFile+":")
		defs = append(defs, types.KnowledgeSymbolDefinition{Source: file + ":" + line, Doc: e.Attrs[AttrDoc]})
	}
	return defs
}
