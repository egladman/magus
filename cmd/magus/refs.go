package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/edit"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
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
			fmt.Fprintln(os.Stderr, "       magus refs --text <pattern> [<path>...] [flags]")
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
		if rf.Text {
			fmt.Fprintln(os.Stderr, "magus refs --text: requires a search pattern")
		} else {
			fmt.Fprintln(os.Stderr, "magus refs: requires a symbol ID or name")
		}
		return errSilent{exitCode: 2}
	}
	if rf.Rename != "" && (rf.Text || rf.Occurrences || rf.Definition || rf.Source) {
		return usagef("magus refs: --rename takes none of --text, --occurrences, --definition, --source")
	}

	if rf.Text {
		// Computed here, once, and passed down: the same shape the symbol-miss
		// branch below uses classify in. inspectWorkspace memoizes globally per
		// process (see helpers.go), so it belongs at the call site that runs once
		// per invocation, not inside a function a test may call several times
		// with different roots.
		var classify classifyFunc
		if ws, wsErr := inspectWorkspace(ctx, root); wsErr == nil {
			classify = ws.ClassifyFiles
		}
		return refsTextCmd(ctx, root, pos[0], pos[1:], noGenerated, rf.Limit, classify)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	// --rename edits files and --definition/--source read them, so those stay local; the
	// plain lookup and --occurrences are what a server answers.
	read := graphRead{Input: pos[0], Occurrences: rf.Occurrences}
	routable := !rf.Refresh && rf.Rename == "" && !rf.Definition && !rf.Source
	var res refsResult
	var g *knowledge.Graph
	if !routable || !askServer(ctx, root, readRefs, &read, &res) {
		// Eager, unlike query and explain: refs always merges symbols, which needs the
		// evaluated projects, and classifies files below.
		ws, err := openWorkspaceForRead(ctx, root)
		if err != nil {
			return err
		}
		if g, err = loadRefsGraph(ctx, ws, globalCfg, rf.Refresh, pos[0]); err != nil {
			return err
		}
		if rf.Rename != "" {
			return refsRenameCmd(ctx, root, opts, g, pos[0], rf.Rename, globalCfg.DryRun)
		}
		res = lookupRefs(ctx, ws, globalCfg, g, read)
	}
	if len(res.Ambiguous) > 0 {
		return refuseAmbiguous(os.Stderr, pos[0], res.Ambiguous)
	}
	out := res.Out
	if !res.Found {
		ans := res.Answer
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
			// a failure here just means classification degrades to "unavailable" below:
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
		miss := refsMiss{name: pos[0], answer: withIndexCause(ctx, root, ans), searched: searched}
		if ans.Text != nil {
			miss.notes = textPresenceNotes(skipped, generated, ans.Text.Files, classifiedFiles, noGenerated)
		}
		if err := reportRefsMiss(os.Stderr, opts, miss); err != nil {
			return err
		}
		emitNearest(os.Stderr, res.Nearest)
		return exitForVerdict(ans.Verdict)
	}

	out.Answer = withIndexCause(ctx, root, out.Answer)

	if rf.Occurrences {
		return emitOccurrences(ctx, root, opts, out, res.Occurrences)
	}
	if rf.Definition || rf.Source {
		defs, _ := g.Definitions(out.Symbol)
		defs.Answer = knowledge.Answer(pos[0], len(defs.Definitions) > 0, res.coverage)
		defs.Answer.IndexCause = out.Answer.IndexCause
		checkDefinitions(resolveRootOrEmpty(root), defs.Label, defs.Definitions, symbolIndexTimes(ctx, root), rf.Source)
		return emitDefinitions(os.Stdout, opts, defs)
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		if err := emitFormatted(opts, out); err != nil {
			return err
		}
		// stderr, because the record on stdout must stay parseable; the exit status is
		// what a script reads, and it is the only channel a structured caller has.
		return reportIndexStaleness(os.Stderr, out.Answer)
	case outputName:
		names := make([]string, 0, len(out.Refs))
		for _, r := range out.Refs {
			names = append(names, r.File)
		}
		if err := emitNames(names); err != nil {
			return err
		}
		return reportIndexStaleness(os.Stderr, out.Answer)
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
		printIndexCause(os.Stdout, out.Answer)
		if err := reportIndexStaleness(os.Stdout, out.Answer); err != nil {
			return err
		}
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
	return reportIndexStaleness(os.Stdout, out.Answer)
}

// refsMiss is what refs learned about a name no symbol node matches.
type refsMiss struct {
	name     string
	answer   types.KnowledgeAnswer
	searched int      // files the text search read
	notes    []string // what the text search skipped or excluded, from textPresenceNotes
}

// reportRefsMiss renders m. Under a structured format its record goes to stdout, so a
// script reads the verdict and the index cause; the verdict block goes to stderr in every
// format.
func reportRefsMiss(stderr io.Writer, opts OutputOptions, m refsMiss) error {
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		if err := emitFormatted(opts, types.KnowledgeRefsOutput{
			Definition:    types.KnowledgeRefsDefinition,
			SchemaVersion: types.KnowledgeSchemaVersion,
			Symbol:        m.name,
			Answer:        m.answer,
		}); err != nil {
			return err
		}
	}
	fmt.Fprintf(stderr, "magus refs: no node matches %q\n", m.name)
	printVerdict(stderr, m.answer, "")
	if t := m.answer.Text; t != nil {
		fmt.Fprintf(stderr, "  not a symbol, but present as TEXT: %d occurrence(s) in %d file(s) of %d searched",
			t.Hits, t.Files, m.searched)
		// One parenthetical, not a second line: a count that does not say what it
		// declined to read, or declined to exclude, is one a reader cannot tell from
		// a small or an unfiltered answer.
		if len(m.notes) > 0 {
			fmt.Fprintf(stderr, " (%s)", strings.Join(m.notes, "; "))
		}
		fmt.Fprintln(stderr)
		fmt.Fprintln(stderr, "  magus indexes symbols, not text; grep is the tool for a string literal or a comment")
	}
	printIndexCause(stderr, m.answer)
	return nil
}

// refsSymbol picks the symbol `refs` answers for. A name several workspace definitions carry
// is refused, listing each candidate's id, rather than answered: answering for whichever
// ranked first shows one symbol's references as though they were the name's, which is a
// narrower answer than the text search refs stands in for, and answering for all of them
// would merge different symbols' sites under one count, which a rename or an edit cannot
// tell apart. The refusal costs one more call, with an exact id that also routes to that
// symbol's shards alone. A name one definition carries picks it; a name none carries, or an
// exact id, goes to the graph's own resolution, which also finds a dependency's symbol.
func refsSymbol(w io.Writer, g *knowledge.Graph, ref string) (string, error) {
	symbol, ambiguous := pickSymbol(g, ref)
	if len(ambiguous) > 0 {
		return "", refuseAmbiguous(w, ref, ambiguous)
	}
	return symbol, nil
}

// pickSymbol is refsSymbol's choice without the refusal: the symbol to answer for, or the
// candidates when ref names more than one workspace definition.
func pickSymbol(g *knowledge.Graph, ref string) (symbol string, ambiguous []string) {
	named := g.SymbolsNamed(ref)
	switch len(named) {
	case 0:
		return ref, nil
	case 1:
		return named[0], nil
	}
	return "", named
}

func refuseAmbiguous(w io.Writer, ref string, named []string) error {
	fmt.Fprintf(w, "magus refs: %q names %d symbols defined in this workspace; ask for one by id:\n", ref, len(named))
	for _, id := range named {
		// Single-quoted: a symbol id holds spaces and backticks, which a paste must not
		// hand to the shell.
		fmt.Fprintf(w, "  %s\n", hint.Refs.With("'"+strings.ReplaceAll(id, "'", `'\''`)+"'"))
	}
	return errSilent{exitCode: exitUsage}
}

// refsResult is what one `magus refs <symbol>` lookup found, built the same way whether a
// server or the local read answered it.
type refsResult struct {
	// Ambiguous lists the workspace symbols the name carries when it names more than one;
	// refs then asks for one by id instead of answering.
	Ambiguous []string                  `json:"ambiguous,omitempty"`
	Found     bool                      `json:"found"`
	Out       types.KnowledgeRefsOutput `json:"out"`
	// Answer is the verdict on a miss: whether the symbol is absent or merely unseen. A
	// hit carries its verdict in Out.Answer.
	Answer  types.KnowledgeAnswer `json:"answer"`
	Nearest string                `json:"nearest,omitempty"`
	// Occurrences is the occurrence read when one was asked for, nil when the probe could
	// not read the symbol indexes at all.
	Occurrences *magus.SymbolOccurrenceRead `json:"occurrences,omitempty"`
	// coverage is what the verdicts were judged from, for the local-only --definition read.
	coverage knowledge.Coverage
}

// lookupRefs looks ref up in g, a graph with ref's symbol shards merged, under cfg.
func lookupRefs(ctx context.Context, ws types.WorkspaceRepository, cfg config.Config, g *knowledge.Graph, read graphRead) refsResult {
	symbol, ambiguous := pickSymbol(g, read.Input)
	if len(ambiguous) > 0 {
		return refsResult{Ambiguous: ambiguous}
	}
	// refs ALWAYS merges the symbol shards, so "not loaded" is never its problem; what it
	// can be missing is an index that was never built, or one built before the definition
	// being asked about existed. Passing HasSymbols() would also be wrong: an exact symbol
	// ID routes to a subset of shards, so it answers about the subset, not the workspace.
	// The declared-index probe is the authority.
	cov := measureSymbolCoverage(ctx, ws, cfg, read.Input, true)
	out, ok := g.Refs(symbol)
	if !ok {
		// This is the miss where a stale index IS the explanation. A name refs cannot
		// resolve is exactly what a build older than the tree would hide, and this path once
		// reported it byte-identically to a typo for something that never existed.
		return refsResult{Answer: knowledge.Answer(read.Input, false, cov), Nearest: g.NearestSymbol(read.Input), coverage: cov}
	}
	// A resolved symbol still carries the coverage verdict: an uncovered project could hold
	// references this list does not show, whether or not it showed any. A stale index
	// caveats a list that has rows and explains one that does not, so the age rides the
	// answer as StaleIndexes either way and only downgrades the empty case. Only the indexes
	// of the symbol's own language could hold its sites, so only they qualify the answer.
	language := g.SymbolLanguage(out.Symbol)
	cov = cov.Narrow(language)
	out.Answer = knowledge.Answer(read.Input, len(out.Refs) > 0, cov)
	res := refsResult{Found: true, Out: out, coverage: cov}
	if read.Occurrences {
		key := strings.TrimPrefix(out.Symbol, types.KindSymbol+":")
		if occ, probed := magus.SymbolOccurrences(ctx, ws, ws.Root(), cfg, slog.Default(), key); probed {
			occ.Unreadable = knowledge.NarrowGaps(occ.Unreadable, language)
			res.Occurrences = &occ
		}
	}
	return res
}

// refsTextCmd implements `magus refs <pattern> --text`: a raw substring search that
// PRINTS matching lines, the shape a guard pipe deny routes a recursive grep to (see
// internal/guard's trimmableMagus). It runs textScan directly (no symbol index, no
// knowledge graph) so it answers on a cold worktree with no index built, which is the
// one thing a grep replacement may never fail to do: textindex.Scan is index-free by
// design for exactly this reason.
//
// Its exit code is grep's: 0 matched, 1 no match, 2 error. Deliberately NOT
// exitForVerdict (see verdict.go): that contract states what magus VERIFIED about a
// SYMBOL (absent=2, unknown=1), and a raw text search asks no such question, so
// reusing it would make "exit 1" mean opposite things depending on a flag on the same
// command. refs' own -o name format already carves out its own exit meaning per mode
// for the same reason (see emitOccurrences); this is that same move made explicit
// for --text rather than left to collide silently with the verdict path.
func refsTextCmd(ctx context.Context, root, pattern string, scopeArgs []string, noGenerated bool, limit int, classify classifyFunc) error {
	searchRoot := resolveRootOrEmpty(root)
	if searchRoot == "" {
		fmt.Fprintln(os.Stderr, "magus refs --text: cannot resolve a workspace root to search")
		return errSilent{exitCode: 2}
	}
	scopes, err := resolveSearchScopes(searchRoot, scopeArgs)
	if err != nil {
		// Exit 2, never 1: a scope magus could not resolve is a search that never ran,
		// and reporting it as "no match" would answer a question nobody asked.
		fmt.Fprintf(os.Stderr, "magus refs --text: %v\n", err)
		return errSilent{exitCode: 2}
	}
	// A root that cannot be listed at all (missing, not a directory, permission
	// denied) is a search that never ran, and must not read as "ran and found
	// nothing" (exit 1). searchableFiles skips an individual entry that vanishes
	// mid-walk (a live checkout's ordinary churn); this is the coarser check that
	// the walk never had anything to do in the first place.
	if _, err := os.ReadDir(searchRoot); err != nil {
		fmt.Fprintf(os.Stderr, "magus refs --text: cannot search %s: %v\n", searchRoot, err)
		return errSilent{exitCode: 2}
	}

	matches, _, skipped, generated, classified, err := textScan(ctx, searchRoot, pattern, scopes, noGenerated, classify)
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus refs --text: %v\n", err)
		return errSilent{exitCode: 2}
	}

	// --limit is what `| head` was reaching for, without the two things head costs: the
	// count of what it cut, and the exit code, which a pipeline takes from the last stage
	// so a match becomes head's 0 and a no-match becomes head's 0 as well.
	//
	// The elision is COUNTED and said out loud on stderr, for the same reason the filtering
	// notes below are: a silent under-report is the one failure that would make this worse
	// than the grep it replaces. Truncation happens at PRINT time, never in the scan, so
	// the match total and the per-file accounting describe the whole search.
	matchedFiles := map[string]bool{}
	shown := matches
	if limit > 0 && len(shown) > limit {
		shown = shown[:limit]
	}
	for _, m := range shown {
		fmt.Printf("%s:%d:%s\n", m.Path, m.Line, m.Text)
	}
	for _, m := range matches {
		matchedFiles[m.Path] = true
	}
	if len(shown) < len(matches) {
		fmt.Fprintf(os.Stderr, "magus refs --text: showing %d of %d matches (--limit); raise --limit or pass 0 for all\n",
			len(shown), len(matches))
	}
	// Same accounting textPresence prints beside a symbol miss, on stderr so stdout
	// stays exactly the match stream a pipe or xargs expects: any filtering must be
	// counted and said out loud, because a silent under-report is the one failure
	// that makes this worse than the grep it replaces.
	if notes := textPresenceNotes(skipped, generated, len(matchedFiles), classified, noGenerated); len(notes) > 0 {
		fmt.Fprintf(os.Stderr, "magus refs --text: %s\n", strings.Join(notes, "; "))
	}

	if len(matches) == 0 {
		return errSilent{exitCode: 1}
	}
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
func emitOccurrences(ctx context.Context, root string, opts OutputOptions, refs types.KnowledgeRefsOutput, occurrences *magus.SymbolOccurrenceRead) error {
	if occurrences == nil {
		// The probe itself failed, so magus cannot say where the symbol appears. Reporting
		// an empty list here would read as "nowhere", which is the one answer it has no
		// basis for.
		fmt.Fprintln(os.Stderr, "magus refs: cannot read the symbol indexes")
		return errSilent{exitCode: 1}
	}
	read := *occurrences
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
	if out.Answer.IndexBehind() {
		if out.Answer.IndexCause = refs.Answer.IndexCause; out.Answer.IndexCause == nil {
			out.Answer = withIndexCause(ctx, root, out.Answer)
		}
	}
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
		if err := emitFormatted(opts, out); err != nil {
			return err
		}
		// stale_files and verified_count are in the record, and a reader who does not
		// compare them against occurrence_count gets a list of ranges that no longer
		// point at the symbol. The notice rides stderr, where it cannot corrupt the
		// record, and the exit status matches the two arms below.
		if out.VerifiedCount < out.OccurrenceCount {
			fmt.Fprint(os.Stderr, unverifiedNotice(out))
			return errSilent{exitCode: 1}
		}
		return nil
	case outputName:
		// file:line:col, the form every editor and `xargs` already understands. Only
		// verified sites: -o name has nowhere to put a status, and emitting an unverified
		// range in a list that looks actionable is exactly the confusion the status exists
		// to prevent.
		var names []string
		for _, f := range out.Files {
			for _, occ := range f.Occurrences {
				if occ.Status == types.SymbolOccurrenceVerified {
					names = append(names, fmt.Sprintf("%s:%d:%d", f.File, occ.Line, occ.Column))
				}
			}
		}
		if err := emitNames(names); err != nil {
			return err
		}
		// Filtering to verified sites is what makes this format safe to pipe, and it is also
		// what makes a wholly stale index print NOTHING: byte-identical to a symbol with no
		// occurrences at all. This is the format a script reads, so the difference has to
		// live in the exit status, which is the only channel it has left.
		if out.VerifiedCount < out.OccurrenceCount {
			fmt.Fprintf(os.Stderr, "magus refs: %d of %d site(s) did not verify and were not listed; refresh with `%s`\n",
				out.OccurrenceCount-out.VerifiedCount, out.OccurrenceCount, hint.GraphBuild)
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
		fmt.Print(unverifiedNotice(out))
		printVerdict(os.Stdout, out.Answer, "")
		return errSilent{exitCode: 1}
	}
	printVerdict(os.Stdout, out.Answer, "")
	return nil
}

// indexedAtFunc reports when the SCIP index covering a workspace-relative file was
// written, and false when no index for it can be found.
type indexedAtFunc func(file string) (time.Time, bool)

// symbolIndexTimes returns an indexedAtFunc over the cached indexes each project declares,
// or nil when the workspace cannot be read. Files under a knowledge.symbols override check
// as unverified; see magus.SymbolIndexTimes.
func symbolIndexTimes(ctx context.Context, root string) indexedAtFunc {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return nil
	}
	projects, err := ws.ListProjects(ctx)
	if err != nil {
		return nil
	}
	times, ok := magus.SymbolIndexTimes(ctx, ws, ws.Root(), globalCfg)
	if !ok {
		return nil
	}
	return func(file string) (time.Time, bool) {
		owner, found := "", false
		for _, p := range projects.Projects {
			if (p.Path == "." || file == p.Path || strings.HasPrefix(file, p.Path+"/")) && (!found || len(p.Path) > len(owner)) {
				owner, found = p.Path, true
			}
		}
		if !found {
			return time.Time{}, false
		}
		at, ok := times[owner]
		return at, ok
	}
}

// checkDefinitions sets each site's Status against the file on disk under root, and
// fills Source when withSource is set. The graph is re-assembled from the working tree on
// every query while the index keeps the positions of the tree it was built over, so a
// digest of today's lines proves nothing; what does is the symbol's name still sitting on
// its start line (else changed) and the file predating its index (else unverified, since
// an edit inside the body moves the end). A site with no end line is checked and printed
// over its declaration line alone.
func checkDefinitions(root, name string, sites []types.KnowledgeDefinitionSite, indexedAt indexedAtFunc, withSource bool) {
	type sourceFile struct {
		lines   [][]byte
		modTime time.Time
	}
	files := map[string]*sourceFile{}
	for i := range sites {
		site := &sites[i]
		if site.StartLine == 0 {
			continue
		}
		file, seen := files[site.File]
		if !seen {
			path := filepath.Join(root, filepath.FromSlash(site.File))
			if data, err := os.ReadFile(path); err == nil {
				file = &sourceFile{lines: types.SplitSourceLines(data)}
				if info, err := os.Stat(path); err == nil {
					file.modTime = info.ModTime()
				}
			}
			files[site.File] = file
		}
		end := max(site.StartLine, site.EndLine)
		if file == nil || end > len(file.lines) {
			site.Status = types.DefinitionUnreadable
			continue
		}
		site.Status = types.DefinitionUnverified
		switch {
		case name != "" && !containsWord(file.lines[site.StartLine-1], name):
			site.Status = types.DefinitionChanged
		case indexedAt != nil:
			if at, ok := indexedAt(site.File); ok && !file.modTime.After(at) {
				site.Status = types.DefinitionVerified
			}
		}
		if withSource {
			site.Source = string(bytes.Join(file.lines[site.StartLine-1:end], []byte("\n")))
		}
	}
}

// containsWord reports whether line holds name delimited by non-identifier bytes, so a
// declaration of F is not confirmed by a line declaring FF.
func containsWord(line []byte, name string) bool {
	isIdent := func(b byte) bool {
		return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= 0x80
	}
	for off := 0; ; {
		i := bytes.Index(line[off:], []byte(name))
		if i < 0 {
			return false
		}
		start, stop := off+i, off+i+len(name)
		if (start == 0 || !isIdent(line[start-1])) && (stop == len(line) || !isIdent(line[stop])) {
			return true
		}
		off = start + 1
	}
}

// definitionRange renders a site as path:start-end, path:start when the end is unknown, or
// the bare path when no line was recorded.
func definitionRange(s types.KnowledgeDefinitionSite) string {
	switch {
	case s.StartLine == 0:
		return s.File
	case s.EndLine == 0:
		return fmt.Sprintf("%s:%d", s.File, s.StartLine)
	}
	return fmt.Sprintf("%s:%d-%d", s.File, s.StartLine, s.EndLine)
}

// emitDefinitions renders `magus refs <symbol> --definition`. It exits 1 when a range no
// longer holds what was indexed or cannot be read: the lines printed for it are not the
// symbol's, and a caller editing by them would edit the wrong text.
func emitDefinitions(w io.Writer, opts OutputOptions, out types.KnowledgeDefinitionsOutput) error {
	var stale, verified int
	for _, s := range out.Definitions {
		switch s.Status {
		case types.DefinitionChanged, types.DefinitionUnreadable:
			stale++
		case types.DefinitionVerified:
			verified++
		}
	}
	staleErr := func() error {
		if stale > 0 {
			return errSilent{exitCode: 1}
		}
		return nil
	}
	// An index older than its project's latest edit may miss a definition added since,
	// which is what reportIndexStaleness fails an answer for. A verified digest proves the
	// site it names, so an answer made only of those carries the notice and exits 0: in a
	// checkout being edited some index is always behind, and an exit that is always 1
	// teaches callers to ignore it.
	settle := func(nw io.Writer) error {
		if verified > 0 && verified == len(out.Definitions) {
			fmt.Fprint(nw, staleIndexNotice(out.Answer))
			return nil
		}
		if err := reportIndexStaleness(nw, out.Answer); err != nil {
			return err
		}
		return staleErr()
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		if err := emitFormatted(opts, out); err != nil {
			return err
		}
		if stale > 0 {
			fmt.Fprintf(os.Stderr, "magus refs: %d definition(s) changed since indexing; refresh with `%s`\n", stale, hint.GraphBuild)
		}
		return settle(os.Stderr)
	case outputName:
		var names []string
		for _, s := range out.Definitions {
			if s.StartLine > 0 && s.Status != types.DefinitionChanged && s.Status != types.DefinitionUnreadable {
				names = append(names, definitionRange(s))
			}
		}
		if err := emitNames(names); err != nil {
			return err
		}
		if stale > 0 {
			fmt.Fprintf(os.Stderr, "magus refs: %d definition(s) changed since indexing and were not listed; refresh with `%s`\n", stale, hint.GraphBuild)
		}
		return staleErr()
	}

	fmt.Fprintf(w, "symbol: %s", out.Symbol)
	if out.Label != "" {
		fmt.Fprintf(w, "  (%s)", out.Label)
	}
	fmt.Fprintln(w)
	if len(out.Definitions) == 0 {
		fmt.Fprintln(w, "no definition in this workspace")
		printVerdict(w, out.Answer, "")
		if out.Answer.Verdict == types.VerdictUnknown {
			return errSilent{exitCode: 1}
		}
		return nil
	}
	for i, s := range out.Definitions {
		if i > 0 && s.Source != "" {
			fmt.Fprintln(w)
		}
		fmt.Fprint(w, definitionRange(s))
		if s.StartLine == 0 {
			fmt.Fprint(w, "  (the index recorded no line in this file)")
		} else {
			fmt.Fprintf(w, "  %s", s.Status)
			if s.EndLine == 0 {
				fmt.Fprint(w, "  (the index recorded no end line; the declaration line alone)")
			}
			if s.Status == types.DefinitionChanged {
				fmt.Fprintf(w, "  (edited since indexing; refresh with `%s`)", hint.GraphBuild)
			}
		}
		fmt.Fprintln(w)
		if s.Source != "" {
			fmt.Fprintln(w, s.Source)
		}
	}
	return settle(w)
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

// refsRenameCmd is `magus refs <symbol> --rename <new>`: glue between the graph, the
// occurrence read, the guard and internal/edit, which owns what a write may do.
func refsRenameCmd(ctx context.Context, root string, opts OutputOptions, g *knowledge.Graph, ref, to string, dryRun bool) error {
	out := types.KnowledgeRenameOutput{
		Definition:    types.KnowledgeRenameDefinition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		To:            to,
	}
	refuse := func(refused ...types.EditRefusal) error {
		out.Refused = refused
		if opts.Format != outputText && opts.Format != outputName {
			if err := emitFormatted(opts, out); err != nil {
				return err
			}
		}
		fmt.Fprintln(os.Stderr, "magus refs: rename refused, nothing written")
		for _, r := range refused {
			fmt.Fprintln(os.Stderr, "  "+edit.FormatRefusal(r))
		}
		return errSilent{exitCode: 1}
	}

	id, err := edit.ResolveRenameSymbol(ref, g.Resolve(ref, 0), func(id string) bool {
		refs, ok := g.Refs(id)
		return ok && len(refs.Defs) > 0
	})
	if err != nil {
		return refuse(types.EditRefusal{Reason: err.Error()})
	}
	refs, _ := g.Refs(id)
	out.Symbol, out.Label = refs.Symbol, refs.Label

	language := g.SymbolLanguage(id)
	blind := renameBlindSpots(id, language, symbolCoverage(ctx, root, id, true))

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	key := strings.TrimPrefix(id, types.KindSymbol+":")
	read, probed := magus.SymbolOccurrences(ctx, ws, ws.Root(), globalCfg, slog.Default(), key)
	if !probed {
		return refuse(append(blind, types.EditRefusal{Reason: "cannot read the symbol indexes"})...)
	}
	for _, gap := range knowledge.NarrowGaps(read.Unreadable, language) {
		blind = append(blind, types.EditRefusal{Reason: fmt.Sprintf("project %s: symbol index %s; rebuild it with `%s`", gap.Project.Path, gap.Describe(), hint.GraphBuild)})
	}
	if len(read.Names) > 0 {
		out.From = read.Names[0]
	}
	sites, refused := edit.RenameSites(read.Files, out.From, to)
	refused = append(refused, edit.RenameCollisions(to, g.Resolve(to, 0), func(id string) []string {
		var files []string
		if refs, ok := g.Refs(id); ok {
			for _, d := range refs.Defs {
				files = append(files, d.File)
			}
		}
		return files
	}, sites)...)
	importers := buzzImporters(g)
	readFile := func(file string) ([]byte, error) {
		return os.ReadFile(filepath.Join(ws.Root(), filepath.FromSlash(file)))
	}
	defs := make([]string, 0, len(refs.Defs))
	for _, d := range refs.Defs {
		defs = append(defs, d.File)
	}
	refused = append(refused, edit.BuzzNameCaptures(to, sites, importers, readFile)...)
	refused = append(refused, edit.BuzzLabelUses(out.From, defs, sites, importers, readFile)...)
	if refused = append(blind, refused...); len(refused) > 0 {
		return refuse(refused...)
	}

	plan := edit.Resolve(ws.Root(), sites, edit.WithGrade(renameGrade(ctx, ws, root, dryRun)))
	if refused := plan.Refused(); len(refused) > 0 {
		return refuse(refused...)
	}
	out.Files, out.Sites = plan.Files(), plan.Spans()
	out.FileCount, out.SiteCount = len(out.Files), len(out.Sites)
	if !dryRun {
		if err := plan.Apply(); err != nil {
			return err
		}
		out.Applied = true
	}

	switch opts.Format {
	case outputText:
	case outputName:
		names := make([]string, 0, len(out.Files))
		for _, f := range out.Files {
			names = append(names, f.Path)
		}
		return emitNames(names)
	default:
		return emitFormatted(opts, out)
	}
	verb := "would rename"
	if out.Applied {
		verb = "renamed"
	}
	fmt.Printf("%s %s to %s: %d site(s) in %d file(s)\n", verb, out.From, out.To, out.SiteCount, out.FileCount)
	for _, s := range out.Sites {
		fmt.Printf("  %s:%d:%d\n", s.Path, s.Start.Line, s.Start.Column)
	}
	if !out.Applied {
		fmt.Println("nothing written (--dry-run)")
	}
	return nil
}

// renameBlindSpots refuses a rename of id, a symbol of language, for every index that could
// hold its sites and is missing or older than its sources: such an index can hold call
// sites the occurrence list never names, and no per-site check sees a site that is not
// listed. An index of another language holds none of them, so it refuses nothing.
func renameBlindSpots(id, language string, cov knowledge.Coverage) []types.EditRefusal {
	answer := knowledge.Answer(id, true, cov.Narrow(language))
	blind := make([]types.EditRefusal, 0, len(answer.Gaps)+len(answer.StaleIndexDetails))
	for _, gap := range answer.Gaps {
		blind = append(blind, types.EditRefusal{Reason: fmt.Sprintf("project %s: symbol index %s; build it with `%s`", gap.Project.Path, gap.Describe(), hint.GraphBuild)})
	}
	for _, stale := range answer.StaleIndexDetails {
		index := "index"
		if stale.Language != "" {
			index = stale.Language + " index"
		}
		blind = append(blind, types.EditRefusal{Reason: fmt.Sprintf("project %s changed after its %s was built; refresh with `%s`", stale.Project, index, hint.GraphBuild)})
	}
	return blind
}

// buzzImporters answers, from g's import edges, which workspace-relative files import file.
func buzzImporters(g *knowledge.Graph) func(file string) []string {
	byTarget := map[string][]string{}
	for _, e := range g.Edges() {
		if e.Relation != types.RelationImports {
			continue
		}
		from, isFile := strings.CutPrefix(e.Source, types.KindFile+":")
		to, toFile := strings.CutPrefix(e.Target, types.KindFile+":")
		if isFile && toFile {
			byTarget[to] = append(byTarget[to], from)
		}
	}
	return func(file string) []string { return byTarget[file] }
}

// renameGrade refuses a declared output, then asks the guard about each rewritten file
// exactly as a host edit hook would (see [guard.JudgeEdit] for which lease that is), so the
// lease and claimed-declaration verdicts cannot differ between the two. The edit replaces
// the whole file, which is what lets the guard place every changed line in its declaration.
// Under a dry run the guard records nothing, so a preview leaves no trace.
//
// rootOverride is the --root ws was loaded with, as typed: ws.Root() can be another
// spelling of the same workspace (symlinks resolved), which the memoized load would read
// as a second one.
func renameGrade(ctx context.Context, ws types.WorkspaceRepository, rootOverride string, dryRun bool) func(string, []byte, []byte) (string, string) {
	deps := guardDependencies(ctx, rootOverride)
	ctx = trail.ContextWithEntryPoint(ctx, types.EntryPointCLI)
	return func(rel string, before, after []byte) (string, string) {
		if reason := declaredOutputRefusal(ctx, ws, rel); reason != "" {
			return "", reason
		}
		v := guard.JudgeEdit(ctx, deps, ws.Root(), rel, before, after, dryRun)
		switch v.Decision {
		case "deny", "ask":
			return v.Rule, strings.TrimSpace(v.Reason)
		}
		return "", ""
	}
}

// declaredOutputRefusal refuses a path the workspace declares as a target's output. A
// classification failure refuses too: an unknown role is not a license to hand-edit.
func declaredOutputRefusal(ctx context.Context, ws types.WorkspaceRepository, path string) string {
	entries, err := ws.ClassifyFiles(ctx, []string{path})
	switch {
	case err != nil:
		return fmt.Sprintf("could not classify it against the workspace's declarations: %v", err)
	case len(entries) != 1:
		return fmt.Sprintf("classifying it against the workspace's declarations gave %d answers, not 1", len(entries))
	}
	e := entries[0]
	if e.Role != types.DiffRoleOutput {
		return ""
	}
	var by []string
	for _, c := range e.Claims {
		if c.Role == types.DiffRoleOutput {
			by = append(by, strings.TrimSuffix(c.Project+":"+c.Target, ":"))
		}
	}
	return "a declared output of " + strings.Join(by, ", ") + ": change its source and regenerate it"
}
