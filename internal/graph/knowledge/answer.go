package knowledge

import "github.com/egladman/magus/types"

// The one place a knowledge verdict is derived.
//
// It lives here, beside SeedsLazyLayer and CouldMatchLazyLayer, because the verdict is a claim
// ABOUT them: `absent` asserts that everything which could have matched was consulted, and
// only those predicates know what could have. The CLI, the MCP tools and the Connect
// GraphService each used to assemble a reason themselves, and they drifted: for the same
// query against the same graph the CLI reported `absent` while MCP reported
// `unknown / symbols-not-loaded`, because one gated on CouldMatchLazyLayer and the other did
// not. None of them derives a verdict now; each reports what it observed and calls Answer.

// Coverage is what a lookup was actually able to consult. Every field is an OBSERVATION,
// never a re-derivation: Seeded is what the caller loaded, not what it should have loaded,
// so a caller that skips the lazy layer cannot also be the one that decides the skip was
// harmless.
type Coverage struct {
	// Seeded reports that the lazily-loaded @symbols shards were merged for this lookup.
	Seeded bool
	// Probed reports that the declared-index probe ran. False means Gaps says nothing: an
	// empty gap list from a failed probe would read as verified coverage.
	Probed bool
	// Gaps are the declared symbol indexes that could not be read.
	Gaps []types.KnowledgeSymbolGap
	// Stale are the built indexes that would be rebuilt for the current sources.
	Stale []types.KnowledgeStaleIndex
}

// For narrows c to the indexes that could hold a symbol of language: a Go symbol's sites
// are in no Buzz index, so a missing or stale Buzz index beside a current Go one says
// nothing about them. An empty language narrows nothing, which is the coverage of a lookup
// that did not resolve to one symbol.
func (c Coverage) For(language string) Coverage {
	if language == "" {
		return c
	}
	out := c
	out.Gaps, out.Stale = GapsFor(c.Gaps, language), nil
	for _, s := range c.Stale {
		if s.CouldHold(language) {
			out.Stale = append(out.Stale, s)
		}
	}
	return out
}

// GapsFor is the gaps that could hold a symbol of language, in Coverage.For's terms, for a
// read that gathered its gaps apart from a Coverage.
func GapsFor(gaps []types.KnowledgeSymbolGap, language string) []types.KnowledgeSymbolGap {
	var out []types.KnowledgeSymbolGap
	for _, g := range gaps {
		if g.CouldHold(language) {
			out = append(out, g)
		}
	}
	return out
}

// SymbolLanguage is the language of the symbol ref resolves to, as the graph recorded it:
// the symbol node's own, else its defining file's. Empty when ref names no symbol or
// neither says, which Coverage.For reads as any language.
func (g *Graph) SymbolLanguage(ref string) string {
	id, ok := g.resolveSymbol(ref)
	if !ok {
		return ""
	}
	if n, ok := g.node(id); ok && n.Attrs[attrLanguage] != "" {
		return n.Attrs[attrLanguage]
	}
	g.ensureAdj()
	for _, e := range g.in[id] {
		if e.Relation != types.RelationDefines {
			continue
		}
		if f, ok := g.node(e.Source); ok && f.Attrs[attrLanguage] != "" {
			return f.Attrs[attrLanguage]
		}
	}
	return ""
}

// Answer classifies a lookup's result against its coverage. input is the query text, used
// only to ask whether the lazy layer was relevant at all.
func Answer(input string, matched bool, cov Coverage) types.KnowledgeAnswer {
	if !CouldMatchLazyLayer(input) {
		// The layer could not have held the answer, so neither its gaps nor its staleness
		// bears on this verdict. Caveating here would point the reader at a layer that was
		// never in scope.
		return types.ClassifyAnswer(matched, "", nil)
	}
	ans := types.ClassifyAnswer(matched, unknownReason(matched, cov), cov.Gaps)
	// Data, not a verdict input: staleness rides every answer, found or empty, so a
	// structured consumer reads the same caveat the text arm prints under the rows.
	if len(cov.Stale) > 0 {
		ans.StaleIndexes = types.StaleIndexProjects(cov.Stale)
		ans.StaleIndexDetails = cov.Stale
	}
	return ans
}

// unknownReason picks the one reason that explains the coverage, most fundamental first: a
// probe that did not run says nothing about a layer that was never loaded, and a layer that
// was never loaded says nothing about how old it is.
func unknownReason(matched bool, cov Coverage) types.KnowledgeUnknownReason {
	switch {
	case !cov.Probed:
		return types.ReasonCoverageUnknown
	case !cov.Seeded:
		return types.ReasonSymbolsNotLoaded
	case !matched && len(cov.Stale) > 0:
		// A stale index cannot hold a definition added since it was built, so a MISS against
		// one is not a verified absence. The sites it did return are still facts, which is
		// why this fires only on a miss.
		//
		// It used to fire only for a lookup whose whole evidence base WAS the index, on the
		// argument that downgrading every empty query would be noise. That argument rested
		// on staleness being measured by mtime, which `format` made permanently true; now
		// that it is the cache's content answer, an empty seeded lookup against an index
		// magus knows is behind has no claim to `absent`, and printing one under a "stale
		// index" banner contradicted the banner in the same breath.
		return types.ReasonIndexStale
	}
	return ""
}
