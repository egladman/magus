package knowledge

import (
	"context"
	"fmt"
	"io"
	"slices"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/libs/clikit/exit"
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

// PrintVerdict writes the block explaining an answer that reported nothing. It prints
// nothing for a found verdict: the rows above it already said so.
//
// searchHint is the command that would search the layer this lookup did not, or "" when
// the caller has no better suggestion than the one it already made.
func PrintVerdict(w io.Writer, ans types.KnowledgeAnswer, searchHint string) {
	switch ans.Verdict {
	case types.VerdictFound:
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
				ans.DescribeStale())
			if ans.IndexCause == nil {
				fmt.Fprintf(w, "  refresh and ask again: %s\n", hint.GraphBuild)
			}
		case types.ReasonSymbolIndexMissing:
		}
		if len(ans.Gaps) > 0 {
			fmt.Fprintf(w, "  outside coverage: %s\n", types.DescribeGaps(ans.Gaps))
		}
		// A cause is what the read did about its index, so no rebuild is owed.
		if c := ans.IndexCause; c != nil {
			fmt.Fprintf(w, "  why: %s\n  hint: %s\n", c.Why, c.Hint)
		} else if slices.ContainsFunc(ans.Gaps, types.KnowledgeSymbolGap.Buildable) {
			fmt.Fprintf(w, "  build the missing index with: %s\n", hint.GraphBuild)
		}
	}
}

// EmitNearest offers the node id a zero-result lookup was probably reaching for,
// through the same "hint: did you mean ..." channel an unknown subcommand uses.
// An empty id (nothing close enough) writes nothing.
//
// It rides UNDER the verdict rather than replacing it: the verdict says what
// magus could search, and a suggestion is a guess about what the reader meant.
// Neither answers the other, and printing only the guess would drop the one
// statement the lookup can actually stand behind.
func EmitNearest(ctx context.Context, id string) {
	if id == "" {
		return
	}
	interactive.Hint(ctx, fmt.Sprintf("did you mean %q?", id))
}

// ExitForVerdict maps a verdict to the process status, for the verbs that treat "nothing
// found" as a failed lookup (refs, explain).
//
// The split is the documented convention, applied literally. An absent verdict is exit 2:
// the request cannot be carried out as stated, because the thing named does not exist and
// magus verified that. An unknown verdict is exit 1: the invocation was fine and the WORK
// could not be done, because a prerequisite artifact is missing. Both used to be 2, which
// left nothing for a script to branch on. A found verdict never reaches here.
func ExitForVerdict(v types.KnowledgeVerdict) error {
	if v == types.VerdictUnknown {
		return exit.Reported(exit.Failure)
	}
	return exit.Reported(exit.Usage)
}

// ExitForQuery maps a search's answer to the process status: 0 when query can answer
// (matches, or a verified absence) and 1 when it cannot.
//
// absent staying 0 is the documented rule and the reason query never adopted the split
// above: an empty result set is a legitimate answer to a SEARCH, and every script running
// `magus query` would break if it became a failure. `unknown` is not an answer at all,
// and it is the one a caller mistakes for "not in the graph" before falling back to a text
// search: the habit the graph exists to replace. It is also the fixable one, by
// hint.GraphBuild. query never exits 2: it takes terms, not a name that must resolve.
//
// The empty-result condition is load-bearing rather than shorthand for the verdict. A
// POPULATED answer carries `unknown` routinely, because a bare free-text query never loads
// the symbol layer, so dropping the condition would make an ordinary successful lookup exit
// non-zero and teach a caller to ignore the status.
func ExitForQuery(out types.KnowledgeQueryOutput) error {
	if out.MatchCount == 0 && out.Answer.Verdict == types.VerdictUnknown {
		return exit.Reported(exit.Failure)
	}
	return nil
}
