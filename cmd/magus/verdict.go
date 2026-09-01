package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

// The third verdict. A graph lookup that returns nothing has two very different
// meanings: magus searched everywhere it should have and the thing is not there, or part
// of the workspace was not searchable and magus cannot say. Collapsing them into one
// empty result is what lets a reader treat a blind spot as a fact, so every answer
// carries which one it is, in the structured output and in the text.
//
// The verbs here inform; none of them decides. An unknown verdict still prints what was
// found, still exits, and names the command that would close the gap.

// symbolCoverage reports what a lookup was able to search. input is the query text,
// seeded reports whether this lookup merged the lazy @symbols shards, and indexOnly marks
// a verb whose whole evidence base is the index (see knowledge.Coverage).
//
// It observes; knowledge.Answer judges. That split is what keeps this surface and the MCP
// tools from reaching different verdicts about the same graph.
//
// Both probes are skipped entirely when the symbol layer could not have held the answer -
// `kind:author` returning nothing has no bearing on a missing or stale symbol index - so an
// ordinary domain query pays nothing for the verdict.
func symbolCoverage(ctx context.Context, root, input string, seeded, indexOnly bool) knowledge.Coverage {
	cov := knowledge.Coverage{Seeded: seeded, IndexOnly: indexOnly}
	if !knowledge.CouldMatchSymbol(input) {
		return cov
	}
	cov.Gaps, cov.Probed = symbolGapsFor(ctx, root)
	cov.Stale = staleIndexProjects(ctx, root)
	return cov
}

// symbolGapsFor lists the projects whose declared symbol index magus could not read, and
// reports whether the probe ran. A probe that could not run must not come back as an
// empty gap list: that reads as verified coverage and would assert the very fact it
// failed to establish.
func symbolGapsFor(ctx context.Context, root string) ([]types.KnowledgeSymbolGap, bool) {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return nil, false
	}
	return magus.SymbolGaps(ctx, ws, ws.Root(), globalCfg, slog.Default())
}

// printVerdict writes the block explaining an answer that reported nothing. It prints
// nothing for a found verdict: the rows above it already said so.
//
// searchHint is the command that would search the layer this lookup did not, or "" when
// the caller has no better suggestion than the one it already made.
func printVerdict(w io.Writer, ans types.KnowledgeAnswer, searchHint string) {
	switch ans.Verdict {
	case types.VerdictAbsent:
		// Deliberately not phrased in terms of symbol indexes. This same line answers a
		// `kind:author` query, where naming the symbol layer would attach a caveat to an
		// answer it has no bearing on. What matters here is the assertion, not the
		// enumeration; the unknown branch below is where naming what is missing pays off.
		fmt.Fprintln(w, "verdict: absent (magus searched everything it could reach, so this is not there)")
	case types.VerdictUnknown:
		fmt.Fprintln(w, "verdict: unknown, not absent")
		switch ans.Reason {
		case types.ReasonSymbolsNotLoaded:
			fmt.Fprintln(w, "  this lookup searched domain entities only, not code symbols")
			if searchHint != "" {
				fmt.Fprintf(w, "  search code symbols with: %s\n", searchHint)
			}
		case types.ReasonCoverageUnknown:
			fmt.Fprintln(w, "  magus could not determine which projects it searched, so this is not a verified absence")
		case types.ReasonIndexStale:
			// The caveat where it is the whole explanation. It used to print only under an
			// answer that found something, and vanish on the miss it actually accounted for.
			fmt.Fprintf(w, "  the symbol index predates the sources it covers, so a definition added since it was built is not in it: %s\n",
				strings.Join(ans.StaleIndexes, ", "))
			fmt.Fprintf(w, "  refresh and ask again: %s\n", hint.GraphBuild)
		}
		if len(ans.Gaps) > 0 {
			fmt.Fprintf(w, "  outside coverage: %s\n", types.DescribeGaps(ans.Gaps))
			fmt.Fprintf(w, "  build the missing index with: %s\n", hint.GraphBuild)
		}
	}
}

// emitNearest offers the node id a zero-result lookup was probably reaching for,
// through the same "hint: did you mean ..." channel an unknown subcommand uses.
// An empty id (nothing close enough) writes nothing.
//
// It rides UNDER the verdict rather than replacing it: the verdict says what
// magus could search, and a suggestion is a guess about what the reader meant.
// Neither answers the other, and printing only the guess would drop the one
// statement the lookup can actually stand behind.
func emitNearest(w io.Writer, id string) {
	if id == "" {
		return
	}
	interactive.Emit(w, fmt.Sprintf("did you mean %q?", id))
}

// exitForVerdict maps a verdict to the process status, for the verbs that treat "nothing
// found" as a failed lookup (refs, explain).
//
// The split is the documented convention, applied literally. An absent verdict is exit 2:
// the request cannot be carried out as stated, because the thing named does not exist and
// magus verified that. An unknown verdict is exit 1: the invocation was fine and the WORK
// could not be done, because a prerequisite artifact is missing. Both used to be 2, which
// left nothing for a script to branch on. A found verdict never reaches here.
func exitForVerdict(v types.KnowledgeVerdict) error {
	if v == types.VerdictUnknown {
		return errSilent{exitCode: 1}
	}
	return errSilent{exitCode: 2}
}
