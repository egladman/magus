package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/edit"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/textindex"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/types"
)

// refsUsage prints the usage block for `magus refs -h`.
func refsUsage(fs *flag.FlagSet) {
	if c, ok := commandAt("refs"); ok {
		fmt.Fprintln(os.Stderr, "Usage: "+c.Usage)
		fmt.Fprintln(os.Stderr, "")
	}
	fmt.Fprintln(os.Stderr, types.KnowledgeRefsDefinition)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The argument is a symbol node ID (symbol:...) or a name that resolves to")
	fmt.Fprintln(os.Stderr, "one. Symbols come from a declared SCIP index (see knowledge.symbols in config).")
	fmt.Fprintln(os.Stderr, "Global flags are also accepted, see `magus -h`.")
	printOwnDefaults(fs)
}

// refsCommand implements `magus refs <symbol>`: list where an ingested code symbol is
// defined and every file that references it. refs is always symbol-seeded, so it
// loads the lazily-loaded @symbols shards. Its output is occurrence-shaped (file:line
// rows), which is why it is a distinct subcommand rather than a `magus query` neighborhood.
func refsCommand(ctx context.Context, root string, args []string) error {
	var rf *flaggen.RefsFlags
	var flags *flag.FlagSet
	pos, err := commandParse("refs", args, func(fs *flag.FlagSet) {
		flags = fs
		rf = flaggen.BindRefs(fs)
		fs.Usage = func() { refsUsage(fs) }
	})
	if err != nil {
		return err
	}
	source, err := refsSourceWindow(rf, flags)
	if err != nil {
		return err
	}
	rf.Source = source.Show
	noGenerated := !rf.Generated
	if len(pos) == 0 {
		if rf.Text {
			slog.ErrorContext(ctx, "requires a search pattern", attr.Notice("magus refs --text"))
		} else {
			slog.ErrorContext(ctx, "requires a symbol ID or name", attr.Notice("magus refs"))
		}
		return exit.Reported(exit.Usage)
	}
	if err := checkRefsInput(rf, flags, pos); err != nil {
		return err
	}
	page, err := refsOccurrencePage(rf, flags, resolveRootOrEmpty(root), pos)
	if err != nil {
		return err
	}
	source.Again = refsAgain(flags, pos, flaggen.FlagRefsMaxLines, flaggen.FlagRefsSkipLines)

	if rf.Text {
		// Computed here, once, and passed down: inspectWorkspace memoizes globally per
		// process (see helpers.go), so it belongs at the call site that runs once
		// per invocation, not inside a function a test may call several times
		// with different roots.
		return refsTextCommand(ctx, root, pos[0], pos[1:], noGenerated, rf.Limit, workspaceClassifier(ctx, root))
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	env := refsEnv(ctx, root)

	// --rename edits files and --definition/--source read them, so those stay local; the
	// plain lookup and --occurrences are what a server answers.
	read := knowledge.GraphRead{Input: pos[0], Occurrences: rf.Occurrences}
	if rf.Refresh && rf.Rename == "" && refreshIndexesNow(ctx, root, "magus refs") {
		return nil
	}
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
		if rf.Rename != "" {
			if g, err = loadRefsGraph(ctx, ws, globalCfg, rf.Refresh, pos[0]); err != nil {
				return err
			}
			return refsRenameCommand(ctx, root, opts, g, pos[0], rf.Rename, globalCfg.DryRun)
		}
		stale := findStaleIndexes(ctx, ws)
		if g, err = loadRefsAnswerGraph(ctx, ws, globalCfg, rf.Refresh, pos[0], stale); err != nil {
			return err
		}
		res = lookupRefs(ctx, ws, globalCfg, g, read, knownStaleCoverage(ctx, ws, globalCfg, stale))
	}
	if !rf.Refresh {
		wait := hint.Refs.With(fmt.Sprintf("'%s'", strings.ReplaceAll(pos[0], "'", `'\''`)), "--refresh")
		res.noteRefresh(refreshInBackground(ctx, root, res.answer(), wait))
	}
	noteShardErrors(ctx, root, "magus refs")
	if len(res.Ambiguous) > 0 {
		return knowledge.RefuseAmbiguous(noticeLines{ctx: ctx, level: slog.LevelError}, pos[0], res.Ambiguous)
	}
	out := res.Out
	if !res.Found {
		return refsMissCommand(ctx, root, opts, pos[0], res, noGenerated)
	}

	out.Answer = withIndexCause(ctx, root, out.Answer)

	if rf.Occurrences {
		return knowledge.EmitOccurrences(ctx, env, opts, out, occurrenceRead(res.Occurrences), source, page)
	}
	if rf.Definition || rf.Source {
		defs, _ := g.Definitions(out.Symbol)
		defs.Answer = knowledge.Answer(pos[0], len(defs.Definitions) > 0, res.coverage)
		defs.Answer.IndexCause = out.Answer.IndexCause
		source.CheckSites(resolveRootOrEmpty(root), defs.Label, defs.Definitions, symbolIndexTimes(ctx, root))
		return knowledge.EmitDefinitions(ctx, env, opts, defs)
	}

	unscoped := out.RefCount
	out = page.ScopedRefs(out)
	if len(page.Scopes) > 0 {
		slog.WarnContext(ctx, page.RefsNote(out.RefCount, unscoped), attr.Notice("magus refs"))
	}
	return emitRefsList(ctx, env, opts, out)
}

// emitRefsList prints the plain reference list in the format asked for.
func emitRefsList(ctx context.Context, env knowledge.RefsEnv, opts output.Options, out types.KnowledgeRefsOutput) error {
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		if err := emitFormatted(opts, out); err != nil {
			return err
		}
		// stderr, because the record on stdout must stay parseable; the exit status is
		// what a script reads, and it is the only channel a structured caller has.
		return knowledge.ReportIndexStaleness(noticeLines{ctx: ctx, level: slog.LevelError}, out.Answer)
	case outputName:
		names := make([]string, 0, len(out.Refs))
		for _, r := range out.Refs {
			names = append(names, r.File)
		}
		if err := emitNames(os.Stdout, names); err != nil {
			return err
		}
		return knowledge.ReportIndexStaleness(noticeLines{ctx: ctx, level: slog.LevelError}, out.Answer)
	}

	return knowledge.WriteRefsText(os.Stdout, env, out)
}

// refsMissCommand reports a name no symbol node matches. `absent` is true of SYMBOLS and
// says nothing about the tree: a string literal, a comment body or a config value is in no
// symbol index, so a bare absent reads as "not in this repository" for exactly the names
// that are. Counting the text occurrences corrects that without minting a fourth verdict:
// the verdict still classifies the symbol lookup, and this rides beside it.
func refsMissCommand(ctx context.Context, root string, opts output.Options, name string, res refsResult, noGenerated bool) error {
	ans := res.Answer
	var searched, skipped, generated int
	var classifiedFiles bool
	// The resolved root, not the --root override: that argument is empty unless the
	// caller passed one, and walking "" searches nothing while reporting nothing.
	if searchRoot := resolveRootOrEmpty(root); searchRoot != "" {
		// A failed classification degrades to "unavailable" in the notes: the search
		// itself must never fail because classification did.
		matches, n, s, g, cls, textErr := textindex.Search(ctx, searchRoot, name, nil, noGenerated, workspaceClassifier(ctx, root))
		searched, skipped, generated, classifiedFiles = n, s, g, cls
		if textErr == nil && len(matches) > 0 {
			presence := knowledge.MissPresence(searchRoot, matches)
			ans.Text = &presence
		}
		if textErr == nil && !noGenerated {
			ans = knowledge.WithoutUnheldGaps(ans, searchRoot, knowledge.TextHits(matches))
		}
	}
	miss := knowledge.RefsMiss{Name: name, Answer: withIndexCause(ctx, root, ans), Searched: searched}
	matchedFiles := 0
	if ans.Text != nil {
		matchedFiles = ans.Text.Files
	}
	miss.Notes = textindex.PresenceNotes(skipped, generated, matchedFiles, classifiedFiles, noGenerated)
	missLines := noticeLines{ctx: ctx, level: slog.LevelError}
	if err := knowledge.ReportRefsMiss(missLines, refsEnv(ctx, root), opts, miss); err != nil {
		return err
	}
	knowledge.EmitNearest(ctx, res.Nearest)
	return knowledge.ExitForVerdict(ans.Verdict)
}

// workspaceClassifier is the loaded workspace's file classifier, or nil when it did not
// load. inspectWorkspace memoizes per process, so asking again costs nothing.
func workspaceClassifier(ctx context.Context, root string) textindex.ClassifyFunc {
	if ws, err := inspectWorkspace(ctx, root); err == nil {
		return ws.ClassifyFiles
	}
	return nil
}

// refsWriters is the part of the refs renderers' environment that needs no request: the
// structured writers. It is a value rather than a call because the renderers it serves
// decide -o name themselves.
var refsWriters = knowledge.RefsEnv{Stdout: os.Stdout, Format: emitFormatted, Names: emitNames}

// refsEnv is what the refs renderers borrow from this command: stdout and --tee, the
// index-cause diagnosis, and the workspace's classifier.
func refsEnv(ctx context.Context, root string) knowledge.RefsEnv {
	env := refsWriters
	env.Root = resolveRootOrEmpty(root)
	env.Notices = noticeLines{ctx: ctx, level: slog.LevelError}
	env.Classify = func() textindex.ClassifyFunc { return workspaceClassifier(ctx, root) }
	env.IndexCause = func(ans types.KnowledgeAnswer) types.KnowledgeAnswer { return withIndexCause(ctx, root, ans) }
	return env
}

// occurrenceRead is the occurrence read the renderers take, nil when the probe failed.
func occurrenceRead(r *magus.SymbolOccurrenceRead) *knowledge.OccurrenceRead {
	if r == nil {
		return nil
	}
	read := knowledge.OccurrenceRead(*r)
	return &read
}

// refsTextCommand implements `magus refs <pattern> --text`: a raw substring search that
// PRINTS matching lines, the shape a guard pipe deny routes a recursive grep to (see
// internal/guard's trimmableMagus). It runs textindex.Search directly (no symbol index, no
// knowledge graph) so it answers on a cold worktree with no index built, which is the
// one thing a grep replacement may never fail to do: textindex.Scan is index-free by
// design for exactly this reason.
//
// Its exit code is grep's: 0 matched, 1 no match, 2 error. Deliberately NOT
// knowledge.ExitForVerdict (see verdict_text.go): that contract states what magus VERIFIED about a
// SYMBOL (absent=2, unknown=1), and a raw text search asks no such question, so
// reusing it would make "exit 1" mean opposite things depending on a flag on the same
// command. refs' own -o name format already carves out its own exit meaning per mode
// for the same reason (see emitOccurrences); this is that same move made explicit
// for --text rather than left to collide silently with the verdict path.
func refsTextCommand(ctx context.Context, root, pattern string, scopeArgs []string, noGenerated bool, limit int, classify textindex.ClassifyFunc) error {
	searchRoot := resolveRootOrEmpty(root)
	if searchRoot == "" {
		slog.ErrorContext(ctx, "cannot resolve a workspace root to search", attr.Notice("magus refs --text"))
		return exit.Reported(exit.Usage)
	}
	scopes, err := textindex.ResolveScopes(searchRoot, scopeArgs)
	if err != nil {
		// Exit 2, never 1: a scope magus could not resolve is a search that never ran,
		// and reporting it as "no match" would answer a question nobody asked.
		slog.ErrorContext(ctx, "", attr.Notice("magus refs --text"), attr.Error(err))
		return exit.Reported(exit.Usage)
	}
	// A root that cannot be listed at all (missing, not a directory, permission
	// denied) is a search that never ran, and must not read as "ran and found
	// nothing" (exit 1). searchableFiles skips an individual entry that vanishes
	// mid-walk (a live checkout's ordinary churn); this is the coarser check that
	// the walk never had anything to do in the first place.
	if _, err := os.ReadDir(searchRoot); err != nil {
		slog.ErrorContext(ctx, "cannot search "+searchRoot, attr.Notice("magus refs --text"), attr.Error(err))
		return exit.Reported(exit.Usage)
	}

	matches, _, skipped, generated, classified, err := textindex.Search(ctx, searchRoot, pattern, scopes, noGenerated, classify)
	if err != nil {
		slog.ErrorContext(ctx, "", attr.Notice("magus refs --text"), attr.Error(err))
		return exit.Reported(exit.Usage)
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
		slog.InfoContext(ctx, fmt.Sprintf("showing %d of %d matches (--limit); raise --limit or pass 0 for all",
			len(shown), len(matches)), attr.Notice("magus refs --text"))
	}
	// Same accounting textPresence prints beside a symbol miss, on stderr so stdout
	// stays exactly the match stream a pipe or xargs expects: any filtering must be
	// counted and said out loud, because a silent under-report is the one failure
	// that makes this worse than the grep it replaces.
	if notes := textindex.PresenceNotes(skipped, generated, len(matchedFiles), classified, noGenerated); len(notes) > 0 {
		slog.InfoContext(ctx, strings.Join(notes, "; "), attr.Notice("magus refs --text"))
	}

	if len(matches) == 0 {
		return exit.Reported(exit.Failure)
	}
	return nil
}

// refsRenameCommand is `magus refs <symbol> --rename <new>`: glue between the graph, the
// occurrence read, the guard and internal/edit, which owns what a write may do.
func refsRenameCommand(ctx context.Context, root string, opts output.Options, g *knowledge.Graph, ref, to string, dryRun bool) error {
	out := types.KnowledgeRenameOutput{
		Definition: types.KnowledgeRenameDefinition,
		APIVersion: types.KnowledgeAPIVersion,
		To:         to,
	}
	refuse := func(refused ...types.EditRefusal) error {
		out.Refused = refused
		if opts.Format != outputText && opts.Format != outputName {
			if err := emitFormatted(opts, out); err != nil {
				return err
			}
		}
		var reasons strings.Builder
		for _, r := range refused {
			reasons.WriteString("\n  " + edit.FormatRefusal(r))
		}
		slog.ErrorContext(ctx, "rename refused, nothing written"+reasons.String(), attr.Notice("magus refs"))
		return exit.Reported(exit.Failure)
	}

	id, err := edit.ResolveRenameSymbol(ref, edit.RenameCandidates(g, ref), func(id string) bool {
		refs, ok := g.Refs(id)
		return ok && len(refs.Defs) > 0
	})
	if err != nil {
		return refuse(types.EditRefusal{Reason: err.Error()})
	}
	refs, _ := g.Refs(id)
	out.Symbol, out.Label = refs.Symbol, refs.Label

	language := g.SymbolLanguage(id)
	blind := edit.RenameBlindSpots(id, language, symbolCoverage(ctx, root, id, true))

	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	key := strings.TrimPrefix(id, types.KindSymbol+":")
	read, probed := workspacegraph.SymbolOccurrences(ctx, ws, ws.Root(), globalCfg, slog.Default(), key)
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
	importers := knowledge.BuzzImporters(g)
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

	plan := edit.Resolve(ws.Root(), sites, edit.WithGrade(edit.RenameGrade(ctx, guardDependencies(ctx, root), ws, dryRun)))
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
		return emitNames(os.Stdout, names)
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

// answer is the verdict on the lookup, a hit's or a miss's.
func (r refsResult) answer() types.KnowledgeAnswer {
	if r.Found {
		return r.Out.Answer
	}
	return r.Answer
}

// loadRefsAnswerGraph is loadRefsGraph, except that with an index behind the
// tree it reads the stored graph as it stands. An edit moves the graph's stamps
// too, and reassembling it costs a read 10-30 s the background refresh pays.
func loadRefsAnswerGraph(ctx context.Context, ws types.WorkspaceRepository, cfg config.Config, refresh bool, ref string, stale []types.KnowledgeStaleIndex) (*knowledge.Graph, error) {
	if refresh || len(stale) == 0 {
		return loadRefsGraph(ctx, ws, cfg, refresh, ref)
	}
	cacheDir, err := magus.ResolveCacheDir(ws.Root(), workspace.WithLoadedConfig(cfg))
	if err != nil {
		return loadRefsGraph(ctx, ws, cfg, refresh, ref)
	}
	store := knowledge.NewStore(cacheDir, true, 0, nil, slog.Default())
	g, err := store.Load(ctx)
	if err != nil {
		return loadRefsGraph(ctx, ws, cfg, refresh, ref)
	}
	g.SetRoot(ws.Root())
	if err := knowledge.MergeStoredSymbols(ctx, store, g, ref); err != nil || !g.HasSymbols() {
		return loadRefsGraph(ctx, ws, cfg, refresh, ref)
	}
	return g, nil
}

// knownStaleCoverage is workspacegraph.Reads.MeasuredCoverage over a staleness verdict the caller
// already holds, since the sweep behind it forks the tool probes.
func knownStaleCoverage(ctx context.Context, ws workspacegraph.ReadWorkspace, cfg config.Config, stale []types.KnowledgeStaleIndex) workspacegraph.CoverageFunc {
	return func(input string, seeded bool) knowledge.Coverage {
		cov := knowledge.Coverage{Seeded: seeded}
		if !knowledge.CouldMatchLazyLayer(input) {
			return cov
		}
		cov.Gaps, cov.Probed = workspacegraph.SymbolGaps(ctx, ws, ws.Root(), cfg, slog.Default())
		cov.Stale = stale
		return cov
	}
}

// noteRefresh files what a background refresh said on the lookup's verdict.
func (r *refsResult) noteRefresh(cause *types.KnowledgeIndexCause) {
	switch {
	case cause == nil:
	case r.Found:
		r.Out.Answer.IndexCause = cause
	default:
		r.Answer.IndexCause = cause
	}
}

// lookupRefs looks ref up in g, a graph with ref's symbol shards merged, under cfg,
// judging the answer by coverage.
func lookupRefs(ctx context.Context, ws types.WorkspaceRepository, cfg config.Config, g *knowledge.Graph, read knowledge.GraphRead, coverage workspacegraph.CoverageFunc) refsResult {
	symbol, ambiguous := knowledge.PickSymbol(g, read.Input)
	if len(ambiguous) > 0 {
		return refsResult{Ambiguous: ambiguous}
	}
	// refs ALWAYS merges the symbol shards, so "not loaded" is never its problem; what it
	// can be missing is an index that was never built, or one built before the definition
	// being asked about existed. Passing HasSymbols() would also be wrong: an exact symbol
	// ID routes to a subset of shards, so it answers about the subset, not the workspace.
	// The declared-index probe is the authority.
	cov := coverage(read.Input, true)
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
		if occ, probed := workspacegraph.SymbolOccurrences(ctx, ws, ws.Root(), cfg, slog.Default(), key); probed {
			occ.Unreadable = knowledge.NarrowGaps(occ.Unreadable, language)
			res.Occurrences = &occ
		}
	}
	return res
}

// symbolIndexTimes returns a func reporting when the SCIP index covering a file was written, over the cached indexes each project declares,
// or nil when the workspace cannot be read. Files under a knowledge.symbols override check
// as unverified; see workspacegraph.SymbolIndexTimes.
func symbolIndexTimes(ctx context.Context, root string) func(file string) (time.Time, bool) {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return nil
	}
	projects, err := ws.ListProjects(ctx)
	if err != nil {
		return nil
	}
	times, ok := workspacegraph.SymbolIndexTimes(ctx, ws, ws.Root(), globalCfg)
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

// commandAgain respells a command from its given flags, leaving out drop.
func commandAgain(with func(...string) string, fs *flag.FlagSet, subject string, drop ...string) string {
	quoted := fmt.Sprintf("'%s'", strings.ReplaceAll(subject, "'", `'\''`))
	return with(slices.Concat([]string{quoted}, givenFlagWords(fs, drop))...)
}

// givenFlagWords respells the given flags, --occurrences first, less drop.
func givenFlagWords(fs *flag.FlagSet, drop []string) []string {
	var words []string
	if flagGiven(fs, flaggen.FlagRefsOccurrences) && !slices.Contains(drop, flaggen.FlagRefsOccurrences) {
		words = append(words, "--"+flaggen.FlagRefsOccurrences)
	}
	fs.Visit(func(f *flag.Flag) {
		if f.Name != flaggen.FlagRefsOccurrences && !slices.Contains(drop, f.Name) {
			words = append(words, flagWords(f)...)
		}
	})
	return words
}

// flagWords respells one given flag as a paste would type it.
func flagWords(f *flag.Flag) []string {
	if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
		return []string{"--" + f.Name}
	}
	if len(f.Name) == 1 {
		return []string{"-" + f.Name, shellArg(f.Value.String())}
	}
	return []string{fmt.Sprintf("--%s=%s", f.Name, shellArg(f.Value.String()))}
}

// shellArg quotes a value a paste would otherwise split or expand.
func shellArg(s string) string {
	if !strings.ContainsAny(s, " \t'\"$`\\*?{}()&;|<>") {
		return s
	}
	return fmt.Sprintf("'%s'", strings.ReplaceAll(s, "'", `'\''`))
}

// refsSourceWindow reads the window off the parsed flags.
// An explicit --before 0 overrides --context, so flagGiven decides.
func refsSourceWindow(rf *flaggen.RefsFlags, fs *flag.FlagSet) (knowledge.RefsWindow, error) {
	sides, ranged, paged := refsWindowFlagsGiven(fs)
	return knowledge.NewRefsWindow(knowledge.RefsWindowOptions{
		Text: rf.Text, Source: rf.Source,
		Context: rf.Context, Before: rf.Before, After: rf.After,
		BeforeGiven: flagGiven(fs, flaggen.FlagRefsBefore), AfterGiven: flagGiven(fs, flaggen.FlagRefsAfter),
		Lines: rf.Lines, MaxLines: rf.MaxLines, SkipLines: rf.SkipLines,
		Sides: sides, Ranged: ranged, Paged: paged,
	})
}

func refsWindowFlagsGiven(fs *flag.FlagSet) (sides, ranged, paged bool) {
	given := func(name string) bool { return flagGiven(fs, name) }
	sides = given(flaggen.FlagRefsContext) || given(flaggen.FlagRefsBefore) || given(flaggen.FlagRefsAfter)
	paged = given(flaggen.FlagRefsMaxLines) || given(flaggen.FlagRefsSkipLines)
	return sides, given(flaggen.FlagRefsLines), paged
}

// checkRefsInput refuses what a form would otherwise drop:
// an answer that ignored part of its question exits 0 and reads as complete.
// It runs after refsSourceWindow, so rf.Source counts what implies it.
func checkRefsInput(rf *flaggen.RefsFlags, fs *flag.FlagSet, pos []string) error {
	if rf.Text {
		for _, name := range []string{flaggen.FlagRefsOccurrences, flaggen.FlagRefsDefinition, flaggen.FlagRefsSource, flaggen.FlagRefsRename, flaggen.FlagRefsRefresh, flaggen.FlagRefsOffset} {
			if flagGiven(fs, name) {
				return exit.Usagef("magus refs: --text searches text with no symbol index, so --%s has nothing to act on; drop one of them", name)
			}
		}
		return nil
	}
	body := rf.Definition || rf.Source && !rf.Occurrences
	switch {
	case rf.Rename != "" && (rf.Occurrences || body):
		return exit.Usagef("magus refs: --rename takes none of --text, --occurrences, --definition, --source")
	case rf.Definition && rf.Occurrences:
		return exit.Usagef("magus refs: --definition lists where the body is and --occurrences every site; drop one (--occurrences --source prints each site's lines)")
	case flagGiven(fs, flaggen.FlagRefsLimit) && !rf.Occurrences:
		return exit.Usagef("magus refs: --limit cuts --occurrences sites or --text matches, and this list has neither; add --occurrences")
	case len(pos) > 1 && (rf.Rename != "" || body):
		return exit.Usagef("magus refs: --rename, --definition and --source take one symbol and no path (got %q); to scope a symbol's sites, use `%s`",
			pos[1], hint.Refs.With(shellArg(pos[0]), "--occurrences", shellArg(pos[1])))
	}
	return nil
}

// refsAgain respells the command that asked, with its paths, less drop.
func refsAgain(fs *flag.FlagSet, pos []string, drop ...string) string {
	again := commandAgain(hint.Refs.With, fs, pos[0], drop...)
	for _, p := range pos[1:] {
		again += " " + shellArg(p)
	}
	return again
}

// refsOccurrencePage reads the paging flags and trailing paths into a page, and spells
// the next command.
func refsOccurrencePage(rf *flaggen.RefsFlags, fs *flag.FlagSet, root string, pos []string) (knowledge.RefsPage, error) {
	return knowledge.NewRefsPage(knowledge.RefsPageOptions{
		Text: rf.Text, Occurrences: rf.Occurrences, OffsetGiven: flagGiven(fs, flaggen.FlagRefsOffset),
		Limit: rf.Limit, Offset: rf.Offset,
		Root: root, ScopeArgs: pos[1:],
		Again: refsAgain(fs, pos, flaggen.FlagRefsLimit, flaggen.FlagRefsOffset),
	})
}

