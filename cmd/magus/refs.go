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
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// refsCmd implements `magus refs <symbol>`: list where an ingested code symbol is
// defined and every file that references it. refs is always symbol-seeded, so it
// loads the lazily-loaded @symbols shards. Its output is occurrence-shaped (file:line
// rows), which is why it is a distinct subcommand rather than a `magus query` neighborhood.
func refsCmd(ctx context.Context, root string, args []string) error {
	var rf *gen.RefsFlags
	// --no-generated stays hand-bound, like watch's --ignore: it is declared in
	// internal/cli/registry.go (Kind: FlagCustom) for the man page, but bound here
	// directly rather than through gen.RefsFlags.
	var noGenerated bool
	pos, err := cmdParse("refs", args, func(fs *flag.FlagSet) {
		rf = gen.BindRefs(fs)
		fs.BoolVar(&noGenerated, "no-generated", false,
			"Exclude declared-output files from the fallback text search entirely, instead of searching them and marking the ones that match")
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
		// what it can be missing is an index that was never built, or one built before the
		// definition being asked about existed. Passing HasSymbols() here would also be
		// wrong for a different reason: an exact symbol ID routes to a subset of shards, so
		// it answers about the subset, not the workspace. The declared-index probe is the
		// authority.
		//
		// This is the miss where a stale index IS the explanation. A name refs cannot
		// resolve is exactly what a build older than the tree would hide, and this path once
		// reported it byte-identically to a typo for something that never existed.
		ans := knowledge.Answer(pos[0], false, symbolCoverage(ctx, root, pos[0], true))
		// `absent` is true of SYMBOLS and says nothing about the tree. A string literal,
		// a comment body or a config value is in no symbol index, so a bare absent here
		// reads as "not in this repository" for exactly the names that are. Counting the
		// text occurrences corrects that without minting a fourth verdict: the verdict
		// still classifies the symbol lookup, and this rides beside it.
		// The resolved root, not the --root override: that argument is empty unless the
		// caller passed one, and walking "" searches nothing while reporting nothing.
		var searched, skipped, generated int
		var classifiedFiles bool
		if searchRoot := resolveRootOrEmpty(root); searchRoot != "" {
			// The same workspace loadKnowledgeGraphForRefs already opened above (memoized
			// by inspectWorkspace), so this costs nothing extra when it is available, and
			// a failure here just means classification degrades to "unavailable" below -
			// the search itself must never fail because classification did.
			var classify classifyFunc
			if ws, wsErr := inspectWorkspace(ctx, root); wsErr == nil {
				classify = ws.ClassifyFiles
			}
			hits, files, n, s, g, cls, textErr := textPresence(ctx, searchRoot, pos[0], noGenerated, classify)
			searched, skipped, generated, classifiedFiles = n, s, g, cls
			if textErr == nil && hits > 0 {
				ans.Text = &types.KnowledgeTextPresence{Hits: hits, Files: files}
			}
		}
		fmt.Fprintf(os.Stderr, "magus refs: no node matches %q\n", pos[0])
		printVerdict(os.Stderr, ans, "")
		if ans.Text != nil {
			fmt.Fprintf(os.Stderr, "  not a symbol, but present as TEXT: %d occurrence(s) in %d file(s) of %d searched",
				ans.Text.Hits, ans.Text.Files, searched)
			// One parenthetical, not a second line: a count that does not say what it
			// declined to read, or declined to exclude, is one a reader cannot tell from
			// a small or an unfiltered answer.
			notes := textPresenceNotes(skipped, generated, ans.Text.Files, classifiedFiles, noGenerated)
			if len(notes) > 0 {
				fmt.Fprintf(os.Stderr, " (%s)", strings.Join(notes, "; "))
			}
			fmt.Fprintln(os.Stderr)
			fmt.Fprintln(os.Stderr, "  magus indexes symbols, not text; grep is the tool for a string literal or a comment")
		}
		if len(ans.Gaps) > 0 {
			fmt.Fprintf(os.Stderr, "  the daemon's auto-indexer also keeps indexes current while `%s` runs\n", hint.ServerStart)
		}
		emitNearest(os.Stderr, g.NearestSymbol(pos[0]))
		return exitForVerdict(ans.Verdict)
	}
	// A resolved symbol still carries the coverage verdict: an uncovered project could
	// hold references this list does not show, whether or not it showed any. A stale index
	// caveats a list that has rows and explains one that does not, so the age rides the
	// answer as StaleIndexes either way and only downgrades the empty case.
	out.Answer = knowledge.Answer(pos[0], len(out.Refs) > 0, symbolCoverage(ctx, root, pos[0], true))

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
		printIndexStaleness(os.Stdout, out.Answer)
		// "nothing uses this" is a NEGATIVE claim, so it follows the verdict the same way
		// an unresolved name does: exit 1 when magus could not verify it. Absent stays 0
		// here, unlike the unresolved branch above: the symbol resolved and its empty
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
	printIndexStaleness(os.Stdout, out.Answer)
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
	out.Answer = types.ClassifyAnswer(len(files) > 0, refs.Answer.Reason, append(append([]types.KnowledgeSymbolGap(nil), refs.Answer.Gaps...), read.Unreadable...))
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
		// what makes a wholly stale index print NOTHING: byte-identical to a symbol with no
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
		// The count alone does not say what to do about it, and the wrong response (edit
		// the good ones, skip the rest) produces a half-renamed tree that still compiles
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
