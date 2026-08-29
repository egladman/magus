package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/types"
)

// refsCmd implements `magus refs <symbol>`: list where an ingested code symbol is
// defined and every file that references it. refs is always symbol-seeded, so it
// loads the lazily-loaded @symbols shards. Its output is occurrence-shaped (file:line
// rows), which is why it is a distinct subcommand rather than a `magus query` neighborhood.
func refsCmd(ctx context.Context, root string, args []string) error {
	var rf *gen.RefsFlags
	pos, err := cmdParse("refs", args, func(fs *flag.FlagSet) {
		rf = gen.BindRefs(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus refs <symbol> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeRefsDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The argument is a symbol node ID (symbol:...) or a name that resolves to")
			fmt.Fprintln(os.Stderr, "one. Symbols come from a declared SCIP index (see knowledge.symbols in config).")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "magus refs: requires a symbol ID or name")
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	g, err := loadKnowledgeGraphForRefs(ctx, root, rf.Refresh, pos[0])
	if err != nil {
		return err
	}
	out, ok := g.Refs(pos[0])
	if !ok {
		// Nothing matched. Whether that is a fact about the workspace or a fact about
		// what magus could see is the whole question, so answer it rather than printing
		// one message for both.
		// refs ALWAYS merges the symbol shards, so "not loaded" is never its problem;
		// what it can be missing is an index that was never built. Passing HasSymbols()
		// here would also be wrong for a different reason: an exact symbol ID routes to a
		// subset of shards, so it answers about the subset, not the workspace. The
		// declared-index probe is the authority.
		gaps, probed := symbolGapsFor(ctx, root)
		var reason types.KnowledgeUnknownReason
		if !probed {
			reason = types.ReasonCoverageUnknown
		}
		ans := types.Answer(false, reason, gaps)
		fmt.Fprintf(os.Stderr, "magus refs: no node matches %q\n", pos[0])
		printVerdict(os.Stderr, ans, "")
		if len(ans.Gaps) > 0 {
			fmt.Fprintf(os.Stderr, "  the daemon's auto-indexer also keeps indexes current while `%s` runs\n", clihint.ServerStart)
		}
		return exitForVerdict(ans.Verdict)
	}
	// A resolved symbol still carries the coverage verdict: an uncovered project could
	// hold references this list does not show, whether or not it showed any.
	gaps, probed := symbolGapsFor(ctx, root)
	var reason types.KnowledgeUnknownReason
	if !probed {
		reason = types.ReasonCoverageUnknown
	}
	out.Answer = types.Answer(len(out.Refs) > 0, reason, gaps)

	if rf.Occurrences {
		return emitOccurrences(ctx, root, opts, out)
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, r := range out.Refs {
			fmt.Println(r.File)
		}
		return nil
	}

	fmt.Printf("symbol: %s", out.Symbol)
	if out.Label != "" {
		fmt.Printf("  (%s)", out.Label)
	}
	fmt.Println()
	if len(out.Defs) > 0 {
		fmt.Println("defined in:")
		for _, d := range out.Defs {
			fmt.Printf("  %s\n", d.File)
		}
	}
	if len(out.Refs) == 0 {
		fmt.Println("no references found")
		printVerdict(os.Stdout, out.Answer, "")
		printIndexStaleness(ctx, os.Stdout, root)
		// "nothing uses this" is a NEGATIVE claim, so it follows the verdict the same way
		// an unresolved name does: exit 1 when magus could not verify it. Absent stays 0
		// here, unlike the unresolved branch above - the symbol resolved and its empty
		// reference list is a real, verified answer, not a request magus could not carry out.
		if out.Answer.Verdict == types.VerdictUnknown {
			return errSilent{exitCode: 1}
		}
		return nil
	}
	fmt.Printf("referenced in %d file(s), %d occurrence(s):\n", out.FileCount, out.RefCount)
	for _, r := range out.Refs {
		fmt.Printf("  %s  (%d)%s\n", r.File, r.Count, linesSuffix(r.Lines))
	}
	// Under the rows, never instead of them. A found answer from a stale index is the
	// dangerous one: it looks complete, and nothing else on this path would say otherwise.
	printIndexStaleness(ctx, os.Stdout, root)
	return nil
}

// emitOccurrences renders `magus refs <symbol> --occurrences`: every exact source range
// the symbol appears at, verified against the tree. refs has already resolved the symbol
// and computed the coverage answer; this re-reads the declared indexes for the ranges the
// graph edge does not keep.
//
// It reports; it does not edit. The output is what a caller needs to make the edit itself,
// which is the same division every other magus verb keeps between naming what a change
// touches and touching it.
func emitOccurrences(ctx context.Context, root string, opts OutputOptions, refs types.KnowledgeRefsOutput) error {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	key := strings.TrimPrefix(refs.Symbol, types.KindSymbol+":")
	read, probed := magus.SymbolOccurrences(ctx, ws, ws.Root(), globalCfg, slog.Default(), key)
	if !probed {
		// The probe itself failed, so magus cannot say where the symbol appears. Reporting
		// an empty list here would read as "nowhere", which is the one answer it has no
		// basis for.
		fmt.Fprintln(os.Stderr, "magus refs: cannot read the symbol indexes")
		return errSilent{exitCode: 1}
	}
	files := read.Files

	out := types.KnowledgeOccurrencesOutput{
		Definition:    types.KnowledgeOccurrencesDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Symbol:        refs.Symbol,
		Label:         refs.Label,
		Names:         read.Names,
		Files:         files,
		FileCount:     len(files),
	}
	if len(read.Names) > 0 {
		out.Name = read.Names[0]
	}
	// The refs verdict describes the refs answer, and this is a different read of a
	// different source: an index that is merely declared satisfies refs' coverage check and
	// can still fail to decode here. So the gaps are recomposed rather than inherited, and
	// an index this read could not open downgrades the verdict even when refs was clean.
	out.Answer = types.Answer(len(files) > 0, refs.Answer.Reason, append(append([]types.KnowledgeSymbolGap(nil), refs.Answer.Gaps...), read.Unreadable...))
	for _, f := range files {
		out.OccurrenceCount += len(f.Occurrences)
		if f.Stale {
			out.StaleFiles++
		}
		for _, occ := range f.Occurrences {
			if occ.Status == types.SymbolOccurrenceVerified {
				out.VerifiedCount++
			}
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		// file:line:col, the form every editor and `xargs` already understands. Only
		// verified sites: -o name has nowhere to put a status, and emitting an unverified
		// range in a list that looks actionable is exactly the confusion the status exists
		// to prevent.
		for _, f := range out.Files {
			for _, occ := range f.Occurrences {
				if occ.Status == types.SymbolOccurrenceVerified {
					fmt.Printf("%s:%d:%d\n", f.File, occ.Line, occ.Column)
				}
			}
		}
		// Filtering to verified sites is what makes this format safe to pipe, and it is also
		// what makes a wholly stale index print NOTHING - byte-identical to a symbol with no
		// occurrences at all. This is the format a script reads, so the difference has to
		// live in the exit status, which is the only channel it has left.
		if out.VerifiedCount < out.OccurrenceCount {
			fmt.Fprintf(os.Stderr, "magus refs: %d of %d site(s) did not verify and were not listed; re-run this project's scip target\n",
				out.OccurrenceCount-out.VerifiedCount, out.OccurrenceCount)
			return errSilent{exitCode: 1}
		}
		// Deliberately NOT exitForVerdict here. The coverage verdict is `unknown` whenever any
		// project declares no index, which in a polyglot workspace is the steady state (a
		// TypeScript project holds no Go symbols), so folding it in would make this format
		// exit non-zero on every successful run and teach a caller to ignore the status. The
		// exit code carries one meaning: sites were found and withheld.
		return nil
	}

	fmt.Printf("symbol: %s", out.Symbol)
	if out.Label != "" {
		fmt.Printf("  (%s)", out.Label)
	}
	fmt.Println()
	if out.OccurrenceCount == 0 {
		fmt.Println("no occurrences found")
		printVerdict(os.Stdout, out.Answer, "")
		if out.Answer.Verdict == types.VerdictUnknown {
			return errSilent{exitCode: 1}
		}
		return nil
	}
	fmt.Printf("name: %s\n", out.Name)
	fmt.Printf("%d occurrence(s) in %d file(s), %d verified:\n", out.OccurrenceCount, out.FileCount, out.VerifiedCount)
	for _, f := range out.Files {
		fmt.Printf("  %s", f.File)
		if f.Stale {
			fmt.Print("  (stale: this file changed after it was indexed)")
		}
		fmt.Println()
		for _, occ := range f.Occurrences {
			fmt.Printf("    %d:%d-%d:%d  %s", occ.Line, occ.Column, occ.EndLine, occ.EndColumn, occ.Status)
			if occ.Definition {
				fmt.Print("  definition")
			}
			if occ.Status != types.SymbolOccurrenceVerified && occ.Text != "" {
				// "want one of": the check is against the whole spelling set, so naming a
				// single expectation would misreport what would actually have verified.
				fmt.Printf("  (found %q, want one of %q)", occ.Text, out.Names)
			}
			fmt.Println()
		}
	}
	if out.VerifiedCount < out.OccurrenceCount {
		// The count alone does not say what to do about it, and the wrong response - edit
		// the good ones, skip the rest - produces a half-renamed tree that still compiles
		// in some languages.
		fmt.Printf("\n%d site(s) in %d file(s) did not verify: the index no longer matches the tree.\n",
			out.OccurrenceCount-out.VerifiedCount, out.StaleFiles)
		fmt.Println("Re-run this project's scip target and try again; sites may also be MISSING from a stale index.")
		printVerdict(os.Stdout, out.Answer, "")
		return errSilent{exitCode: 1}
	}
	printVerdict(os.Stdout, out.Answer, "")
	return nil
}

// linesSuffix renders a capped line list as " lines 5,8,12", or "" when absent.
func linesSuffix(lines []int) string {
	if len(lines) == 0 {
		return ""
	}
	parts := make([]string, len(lines))
	for i, ln := range lines {
		parts[i] = strconv.Itoa(ln)
	}
	return "  lines " + strings.Join(parts, ",")
}
