package knowledge

import (
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// nearest.go answers the question an empty result leaves open: was the term a
// typo? A miss that offers nothing back reads as "not in the graph" and sends
// the reader to grep, which is the habit the graph surface exists to replace.
//
// Matching itself is untouched. Nothing here can put a node into a result set -
// these run only after a lookup has already reported nothing - because a near
// miss admitted as a match would turn "0 matches" from a fact into a maybe, and
// the verdict is only worth printing while it means what it says.

// NearestNode returns the id of the node whose name is a typo away from input's
// single free-text term, or "" when nothing is that close. The query's kind
// filters still apply, so a suggestion is a node the query would have accepted.
//
// Only a single bare term is answered: with two terms there is no one thing the
// reader misspelled, and a wildcard that matched nothing is a pattern to widen
// rather than a name to correct.
func (g *Graph) NearestNode(input string) string {
	q := parseQuery(input)
	if len(q.terms) != 1 || hasWildcard(q.terms[0]) {
		return ""
	}
	pos, neg := q.fields["kind"], q.negFields["kind"]
	return g.nearest(q.terms[0], func(kind string) bool {
		if len(pos) > 0 && !matchesKind(kind, pos) {
			return false
		}
		return !matchesKind(kind, neg)
	})
}

// NearestSymbol is NearestNode restricted to code symbols, for refs - which
// resolves nothing else, so suggesting a target or a doc there would name
// something refs would miss a second time.
func (g *Graph) NearestSymbol(ref string) string {
	return g.nearest(ref, func(kind string) bool { return kind == types.KindSymbol })
}

// nearest scans every node kindOK admits and returns the id of the closest by
// edit distance, "" when none is within hint's threshold.
//
// Ties break the way resolution already breaks them - kindRank first, then the
// lower id - so a domain entity outranks a doc heading that happens to repeat
// its name, and the answer is stable across runs whatever order the node map
// yields. Without the kind bias `explain buld` named a markdown anchor called
// "build" instead of the target.
func (g *Graph) nearest(term string, kindOK func(kind string) bool) string {
	if term == "" {
		return ""
	}
	folded := strings.ToLower(term)
	limit := hint.Threshold(term)
	want := utf8.RuneCountInString(folded)
	best, bestDist, bestRank := "", limit+1, 0
	for id, n := range g.nodes {
		if !kindOK(n.Kind) {
			continue
		}
		rank := kindRank(n.Kind)
		for _, form := range nameForms(id, n.Label) {
			// A length difference is a lower bound on the edit distance, so a form
			// outside the window cannot beat the threshold. The window costs nothing
			// in recall and skips the distance computation for most of the graph -
			// which is what keeps this affordable against the symbol layer.
			if gap := utf8.RuneCountInString(form) - want; gap > limit || gap < -limit {
				continue
			}
			d := hint.Distance(folded, strings.ToLower(form))
			if d < bestDist || (d == bestDist && (rank > bestRank || (rank == bestRank && id < best))) {
				best, bestDist, bestRank = id, d, rank
			}
		}
	}
	if bestDist > limit {
		return ""
	}
	return best
}

// nameForms returns the strings a node answers to: its id without the kind
// prefix, its label, and the last path segment of each.
//
// The raw id is not among them. Distance over "symbol:gomod github.com/... /adoptionRun."
// is dominated by the prefix and the module path, so the one-character slip a
// reader actually made in the trailing name would score dozens of edits and
// never reach the threshold.
func nameForms(id, label string) []string {
	forms := make([]string, 0, 4)
	add := func(s string) {
		if s == "" {
			return
		}
		for _, have := range forms {
			if have == s {
				return
			}
		}
		forms = append(forms, s)
	}
	for _, s := range []string{trimKind(id), label} {
		add(s)
		add(leafOf(s))
	}
	return forms
}

// trimKind drops the "<kind>:" prefix every node id carries.
func trimKind(id string) string {
	if _, rest, ok := strings.Cut(id, ":"); ok {
		return rest
	}
	return id
}

// leafOf is the last path or qualifier segment of a node name: "build" out of
// ".:build", "guard_shell.go" out of "cmd/magus/guard_shell.go".
func leafOf(s string) string {
	if i := strings.LastIndexAny(s, "/:"); i >= 0 {
		return s[i+1:]
	}
	return s
}
