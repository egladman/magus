package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// The staleness line refs, query and explain print under an answer drawn from a symbol
// index older than the sources it describes.
//
// It exists because the alternative was prose. "If refs reports a project not-indexed, run
// `magus graph build` and ask again" is written in a skill and in a guard advisory, and a
// rule that lives only in text is a rule with roughly even odds. This says it where the
// answer is, on every host, with nothing to wire and no hook to install.
//
// It never withholds and never fails a lookup. The rows print first and this is one line
// under them, because a stale index still holds true facts: it holds FEWER of them, and
// the failure it prevents is reading a short list as a complete one. printVerdict covers
// the neighbouring case, an answer that found NOTHING; this one speaks under an answer
// that found something and may be missing the rest.
//
// Freshness is the CACHE's question, asked of the cache. It used to be two stats: any
// source file with an mtime past the index's. That was not merely approximate, it was
// unclearable. `format` and `generate` rewrite files with identical bytes, `go-build`
// chains through `format`, and a scip run that replays does not rewrite the index, so
// mtimes advanced while content did not and `magus graph build` could never clear the
// staleness this banner reported. The cache compares content, so a rewrite that changes no
// bytes is no longer a change, and the remedy the banner names is one that works.

// printIndexStaleness writes the staleness line under an answer, or nothing when every
// built index is current.
//
// It reads the ANSWER rather than probing again. Probing here printed the banner under
// verdicts it had no bearing on: a `kind=author` lookup cannot reach the symbol layer, so
// Answer drops staleness from its scope and says `absent`, and this line then contradicted
// it in the next breath. One observation, two renderings.
//
// Silent when the verdict already IS the staleness: printVerdict's index-stale arm names
// the same projects and the same refresh, and the same fact in two vocabularies teaches a
// reader to skip both.
//
// Text only, and the callers are all inside their text arm already: a structured caller
// reads coverage off the answer record, and a line appended to json would corrupt it.
func printIndexStaleness(w io.Writer, ans types.KnowledgeAnswer) {
	if ans.Reason == types.ReasonIndexStale {
		return
	}
	if notice := staleIndexNotice(ans.StaleIndexes); notice != "" {
		fmt.Fprint(w, notice)
	}
}

// staleIndexNotice renders the line, or "" for an empty list. Split from the probe so the
// two halves are testable apart: what is stale is the cache's answer, and what to say about
// it is not.
func staleIndexNotice(stale []string) string {
	if len(stale) == 0 {
		return ""
	}
	return fmt.Sprintf("\nstale index: %s changed since %s last indexed %s, so this answer may be missing sites in %s.\n",
		plural(len(stale), "a project", "projects"), hint.GraphBuild,
		plural(len(stale), "it", "them"), strings.Join(stale, ", ")) +
		fmt.Sprintf("  refresh and ask again: %s\n", hint.GraphBuild)
}

// plural picks a word by count. Two call sites in one sentence, so the alternative is an
// interpolation that reads worse than the branch.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// staleIndexProjects lists, workspace-relative and sorted, the projects whose built symbol
// index would be rebuilt for the current sources.
//
// Silent on every uncertainty, which is the same contract every other advisory on this
// surface keeps. A project with NO index is deliberately not reported here: that is the
// gap probe's answer, printVerdict already renders it as "outside coverage", and one fact
// stated twice in two vocabularies teaches a reader to skip both.
//
// The verdict comes from SymbolIndexStatus, the same probe `magus status` prints, so the
// banner and the status table cannot disagree about one index. The concrete type is what
// carries it: Inspect returns a *magus.Magus behind the domain interface, and the freshness
// question needs the cache, which no domain interface exposes.
func staleIndexProjects(ctx context.Context, root string) []string {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil || ws == nil {
		return nil
	}
	m, ok := ws.(*magus.Magus)
	if !ok {
		return nil
	}
	var stale []string
	for _, s := range m.SymbolIndexStatus(ctx) {
		if s.Freshness != types.SymbolIndexStale {
			continue
		}
		path := s.Project.Path
		if path == "" {
			path = "."
		}
		stale = append(stale, path)
	}
	slices.Sort(stale)
	return stale
}

// staleGraphAdvice is what the guard says to a graph read about to answer from an index
// older than the tree, or "" when every built index is current.
func staleGraphAdvice(ctx context.Context) string {
	stale := staleIndexProjects(ctx, "")
	if len(stale) == 0 {
		return ""
	}
	return fmt.Sprintf("magus workspace: run `%s` first, then ask again. The answer you are about to get is drawn from an index built before the current sources.\n", hint.GraphBuild) +
		fmt.Sprintf("%s changed since %s last indexed %s: %s. A symbol added or moved since then is missing from the answer, and a lookup that misses it reports \"unknown, not absent\" rather than nothing being there.\n",
			plural(len(stale), "One project", "Several projects"), hint.GraphBuild, plural(len(stale), "it", "them"), strings.Join(stale, ", ")) +
		"This is an advisory: a stale index still holds true facts, so the read is worth running either way."
}
