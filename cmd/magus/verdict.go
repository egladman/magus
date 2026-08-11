package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/types"
)

// The third verdict. A graph lookup that returns nothing has two very different
// meanings: magus searched everywhere it should have and the thing is not there, or part
// of the workspace was not searchable and magus cannot say. Collapsing them into one
// empty result is what lets a reader treat a blind spot as a fact, so every empty answer
// carries which one it is, in the structured output and in the text.
//
// The verbs here inform; none of them decides. An unknown verdict still prints what was
// found, still exits, and names the command that would close the gap.

// symbolGapsFor lists the projects whose declared symbol index magus could not read.
//
// Call it only on an empty result. It stats one file per declared index and parses each
// one it finds, which is cheap but not free, and a lookup that returned something has
// nothing to explain. inspectWorkspace is memoized, so the workspace itself is already
// resolved by the time any caller gets here.
func symbolGapsFor(ctx context.Context, root string) []types.KnowledgeSymbolGap {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		// The verdict is an aid to reading an empty answer, never the reason a command
		// fails. A workspace that will not inspect has already failed louder elsewhere.
		return nil
	}
	return magus.SymbolGaps(ctx, ws, ws.Root(), globalCfg, slog.Default())
}

// gapList renders the uncovered projects as "libs/api (not-indexed), docs (unreadable)".
func gapList(gaps []types.KnowledgeSymbolGap) string {
	parts := make([]string, len(gaps))
	for i, g := range gaps {
		detail := g.Detail
		if detail == "" {
			detail = string(g.State)
		}
		parts[i] = g.Project.Display() + " (" + detail + ")"
	}
	return strings.Join(parts, ", ")
}

// printSymbolAnswer writes the verdict block explaining an empty result. It prints
// nothing for a found verdict: the rows above it already said so.
//
// searchHint is the command that would search the layer this lookup did not, or "" when
// the caller has no better suggestion than the one it already made.
func printSymbolAnswer(w io.Writer, ans types.KnowledgeAnswer, searchHint string) {
	switch ans.Verdict {
	case types.VerdictAbsent:
		// Deliberately not phrased in terms of symbol indexes. This same line answers a
		// `kind:author` query, where naming the symbol layer would attach a caveat to an
		// answer it has no bearing on. What matters here is the assertion, not the
		// enumeration; the unknown branch below is where naming what is missing pays off.
		fmt.Fprintln(w, "verdict: absent (magus searched everything it could reach, so this is not there)")
	case types.VerdictUnknown:
		fmt.Fprintln(w, "verdict: unknown, not absent")
		if ans.Reason == types.ReasonSymbolsNotLoaded {
			fmt.Fprintln(w, "  this lookup searched domain entities only, not code symbols")
			if searchHint != "" {
				fmt.Fprintf(w, "  search code symbols with: %s\n", searchHint)
			}
		}
		if len(ans.Uncovered) > 0 {
			fmt.Fprintf(w, "  outside coverage: %s\n", gapList(ans.Uncovered))
			fmt.Fprintf(w, "  build the missing index with: %s\n", clihint.GraphBuild)
		}
	}
}

// answerExit maps a verdict to the process status, for the verbs that treat "nothing
// found" as a failed lookup (refs, explain).
//
// The split is the documented convention, applied literally. An absent verdict is exit 2:
// the request cannot be carried out as stated, because the thing named does not exist and
// magus verified that. An unknown verdict is exit 1: the invocation was fine and the WORK
// could not be done, because a prerequisite artifact is missing. Both used to be 2, which
// left nothing for a script to branch on.
func answerExit(ans types.KnowledgeAnswer) error {
	if ans.Verdict == types.VerdictUnknown {
		return errSilent{exitCode: 1}
	}
	return errSilent{exitCode: 2}
}
