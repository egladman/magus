package knowledge

import "github.com/egladman/magus/types"

// The one place a knowledge verdict is derived.
//
// It lives here, beside SeedsLazyLayer and CouldMatchLazyLayer, because the verdict is a claim
// ABOUT them: `absent` asserts that everything which could have matched was consulted, and
// only those predicates know what could have. The CLI, the MCP tools and the Connect
// GraphService each used to assemble a reason themselves, and they drifted - for the same
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
	// Probed reports that the declared-index probe ran. False means Gaps says nothing - an
	// empty gap list from a failed probe would read as verified coverage.
	Probed bool
	// Gaps are the projects whose declared symbol index could not be read.
	Gaps []types.KnowledgeSymbolGap
	// Stale are the workspace-relative projects whose built index predates its sources.
	Stale []string
	// IndexOnly marks a lookup whose ENTIRE evidence base is the symbol index, so a stale
	// index leaves a miss unverifiable. `magus refs` is the one: it resolves symbol nodes
	// and consults nothing else. A general query reads many layers, and downgrading every
	// empty one in an actively edited tree would make the verdict noise a caller learns to
	// ignore - the same trap refs' -o name exit code documents.
	IndexOnly bool
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
	ans := types.ClassifyAnswer(matched, reasonFor(matched, cov), cov.Gaps)
	// Data, not a verdict input: staleness rides every answer, found or empty, so a
	// structured consumer reads the same caveat the text arm prints under the rows.
	ans.StaleIndexes = cov.Stale
	return ans
}

// reasonFor picks the one reason that explains the coverage, most fundamental first: a
// probe that did not run says nothing about a layer that was never loaded, and a layer that
// was never loaded says nothing about how old it is.
func reasonFor(matched bool, cov Coverage) types.KnowledgeUnknownReason {
	switch {
	case !cov.Probed:
		return types.ReasonCoverageUnknown
	case !cov.Seeded:
		return types.ReasonSymbolsNotLoaded
	case cov.IndexOnly && !matched && len(cov.Stale) > 0:
		// A stale index cannot hold a definition added since it was built, so a MISS against
		// one is not a verified absence. The sites it did return are still facts, which is
		// why this fires only on a miss.
		return types.ReasonIndexStale
	}
	return ""
}
