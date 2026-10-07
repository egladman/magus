package knowledge

import (
	"cmp"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/prose"
	"github.com/egladman/magus/types"
)

// ProseOptions scopes [Graph.Prose].
type ProseOptions struct {
	// Generated holds the workspace-relative paths that are generated output. Generated and
	// test code is never judged.
	Generated map[string]bool
}

// Prose judges the doc comment and name of every symbol g defines with prose.Judge; g must
// carry its symbols. judged counts the symbols read per language, so a language with no entry
// contributed nothing rather than passed. Findings are ordered by source then rule, so two
// runs over one graph agree byte for byte.
func (g *Graph) Prose(o ProseOptions) (findings []types.ProseFinding, judged map[string]int) {
	x := newNamingIndex(g, ConformanceChange{Generated: o.Generated})
	judged = map[string]int{}
	var ids []string
	for id, n := range g.nodes {
		if n.Kind == types.KindSymbol {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	for _, id := range ids {
		n := g.nodes[id]
		file, _, _ := strings.Cut(n.Source, ":")
		if file == "" || isTestSource(file) || x.generated(file) {
			continue
		}
		language := n.Attrs[attrLanguage]
		judged[language]++
		s := prose.Symbol{Name: n.Label, Doc: n.Attrs[AttrDoc]}
		if d := x.byID[id]; d != nil {
			s.Callable, s.Owner = d.callable(), d.owner
		}
		for _, f := range prose.Judge(s) {
			findings = append(findings, types.ProseFinding{
				Node: id, Source: n.Source, Language: language,
				Rule: string(f.Rule), Message: f.Message, Match: f.Match,
			})
		}
	}
	// Stable: within one source the node ID order and Judge's own order break ties.
	slices.SortStableFunc(findings, func(a, b types.ProseFinding) int {
		return cmp.Or(cmp.Compare(a.Source, b.Source), cmp.Compare(a.Rule, b.Rule))
	})
	return findings, judged
}
