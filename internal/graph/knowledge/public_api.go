package knowledge

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// PublicAPI lists each dir's symbols that a non-test file outside that dir references:
// functions, methods, types and top-level values a non-test file in the dir defines. Fields
// are left out, since their type stands for them, as are locals and namespaces. Sorted by
// UsedBy descending, then ID. A dir whose symbols the graph lacks yields nothing; Explain
// says so with publicAPIGap.
func (g *Graph) PublicAPI(dirs []string) []types.PublicSymbol {
	g.ensureAdj()
	var out []types.PublicSymbol
	seen := map[[2]string]bool{}
	for fid, dir := range g.sourceFilesIn(dirs) {
		for _, e := range g.out[fid] {
			key := [2]string{dir, e.Target}
			if e.Relation != types.RelationDefines || seen[key] || !apiKind(e.Target) {
				continue
			}
			seen[key] = true
			n := g.usedOutside(e.Target, dir)
			if n == 0 {
				continue
			}
			sym := g.nodes[e.Target]
			out = append(out, types.PublicSymbol{
				ID: e.Target, Label: sym.Label, Dir: dir, Signature: sym.Attrs[AttrSignature], UsedBy: n,
			})
		}
	}
	slices.SortFunc(out, func(a, b types.PublicSymbol) int {
		return cmp.Or(cmp.Compare(b.UsedBy, a.UsedBy), cmp.Compare(a.ID, b.ID), cmp.Compare(a.Dir, b.Dir))
	})
	return out
}

// usedOutside counts the distinct sources outside dir, test files aside, with a references
// edge to id. The caller holds the adjacency index.
func (g *Graph) usedOutside(id, dir string) int {
	from := map[string]bool{}
	for _, e := range g.in[id] {
		ref := strings.TrimPrefix(e.Source, types.KindFile+":")
		if e.Relation == types.RelationReferences && !types.IsTestPath(ref) && path.Dir(ref) != dir {
			from[ref] = true
		}
	}
	return len(from)
}

// sourceFilesIn maps the ID of each non-test file directly in one of dirs to that dir.
func (g *Graph) sourceFilesIn(dirs []string) map[string]string {
	want := map[string]bool{}
	for _, d := range dirs {
		want[d] = true
	}
	out := map[string]string{}
	for id, n := range g.nodes {
		if n.Kind != types.KindFile || types.IsTestPath(n.Source) {
			continue
		}
		if d := path.Dir(n.Source); want[d] {
			out[id] = d
		}
	}
	return out
}

// apiKind reports whether a symbol is a function, method, type or top-level value.
func apiKind(id string) bool {
	p, ok := parseDescriptors(id)
	if !ok {
		return false
	}
	k := neutralKind(afterNamespace(p))
	return k != "" && k != declField
}

// publicAPIGap is the sentence Explain prints in place of dir's public API when the graph
// holds no symbol of the dir's language that a file in it defines, or "" when it holds one or
// the dir has no source in an indexed language.
func (g *Graph) publicAPIGap(dir string) string {
	lang := g.nodes[dirID(dir)].Attrs[types.AttrLanguage]
	if lang == "" {
		return ""
	}
	for fid := range g.sourceFilesIn([]string{dir}) {
		if slices.ContainsFunc(g.out[fid], func(e types.KnowledgeEdge) bool { return e.Relation == types.RelationDefines }) {
			return ""
		}
	}
	return fmt.Sprintf("the graph holds no %s symbols for %s; `%s` indexes them", lang, dir, hint.GraphBuild)
}
