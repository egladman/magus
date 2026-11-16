package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/embed"
	"github.com/egladman/magus/types"
)

// query/explain/path are the knowledge-graph retrieval verbs. They reuse
// prior-art vocabulary (graph tooling generally) and sit on the same cache-first
// substrate as `magus graph export`. query resolves terms to nodes and returns
// the neighborhood; explain shows one node's context; path connects two nodes.
//
// query also doubles as the retrieval subcommand for target-output reference ids through
// an EXPLICIT `output` subcommand: `magus query output out1a2b3c` prints that
// execution's captured output instead of searching the graph. It is a subcommand,
// not a shape-routed positional, so a free-text search term can never collide with a
// ref id (`magus query refactor` always searches the graph).

// defaultLogViewerURL is the hosted, data-agnostic log viewer that `magus query
// output <ref> --open` points a browser at, with the captured output delivered
// PRIVATELY in a URL fragment (never uploaded). Override with --url for a self-hosted
// mirror.
const defaultLogViewerURL = "https://eli.gladman.cc/magus/console/logs/"

// splitQueryNegations pulls the query grammar's negation terms (-kind:op, -docker) out
// of args before flag parsing. A negation shares the flag prefix, so stdlib flag.Parse
// rejects it as an unknown flag: the documented syntax was unreachable from the CLI. A
// dash token is a term rather than a flag exactly when the full query flag set does not
// know its name; registered flags, their values, "-name=value" spellings (left for
// flag.Parse to report, since the grammar spells fields with a colon), and everything
// after "--" pass through untouched.
func splitQueryNegations(args []string) (kept, negations []string) {
	fs := flag.NewFlagSet("query-prescan", flag.ContinueOnError)
	flaggen.BindFlags(fs, &globalCfg)
	bindDisplayFlags(fs)
	flaggen.BindQuery(fs, flaggen.QueryDefaults{URL: defaultLogViewerURL})
	flags, positionals := partitionFlags(fs, args)
	kept = make([]string, 0, len(args))
	for i := 0; i < len(flags); i++ {
		a := flags[i]
		if len(a) >= 2 && a[0] == '-' && !strings.ContainsRune(a, '=') {
			name := strings.TrimLeft(a, "-")
			// flag.Parse answers -h and -help itself without registering them, so a lookup
			// misses them; read as negations they searched the graph for "-h".
			if name == "h" || name == "help" {
				kept = append(kept, a)
				continue
			}
			if f := fs.Lookup(name); f == nil {
				negations = append(negations, a)
				continue
			} else if !flagIsBool(f) && i+1 < len(flags) {
				// A value flag's value may itself start with a dash; consume it
				// paired, the same way partitionFlags did, so it is never read
				// as a negation.
				kept = append(kept, a, flags[i+1])
				i++
				continue
			}
		}
		kept = append(kept, a)
	}
	return append(kept, positionals...), negations
}

func queryCommand(ctx context.Context, root string, args []string) error {
	args, negations := splitQueryNegations(args)
	var qf *flaggen.QueryFlags
	pos, err := commandParse("query", args, func(fs *flag.FlagSet) {
		qf = flaggen.BindQuery(fs, flaggen.QueryDefaults{URL: defaultLogViewerURL})
		fs.Usage = func() { printQueryUsage(fs) }
	})
	if err != nil {
		return err
	}
	// Appended rather than interleaved: the grammar joins terms into one string, so
	// relative order between a positive term and a negation carries no meaning.
	pos = append(pos, negations...)

	// Output-reference retrieval is an EXPLICIT subcommand (`magus query output <ref>`), not a
	// shape-routed positional, so a search term can never collide with a ref id.
	if qf.Stdin {
		return queryOutputStdin(ctx, qf, pos)
	}
	if len(pos) >= 1 && pos[0] == hint.QueryOutput.Leaf() {
		return queryOutputCommand(ctx, root, qf, pos)
	}
	// `magus query invocation <id>`, the sibling of `query output <ref>`, and explicit for
	// the same reason: an id is shape-routed nowhere, so a search term cannot collide with one.
	if len(pos) >= 1 && pos[0] == hint.QueryInvocation.Leaf() {
		return queryInvocationCommand(ctx, root, qf, pos)
	}
	if err := checkQuerySearch(ctx, qf, pos); err != nil {
		return err
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	input := strings.Join(pos, " ")
	for _, k := range splitCSV(qf.Kind) {
		input = fmt.Sprintf("%s kind=%s", input, k)
	}

	read := knowledge.GraphRead{Input: input, Budget: qf.Budget, Nearest: opts.Format == outputText}
	if qf.Refresh && !qf.Global && refreshIndexesNow(ctx, root, "magus query") {
		return nil
	}
	var res knowledge.QueryResult
	if qf.Refresh || qf.Global || !askServer(ctx, root, readQuery, &read, &res) {
		if res, err = readQueryLocally(ctx, root, qf, read); err != nil {
			return err
		}
	}
	out := res.Out
	if !qf.Refresh && !qf.Global {
		if cause := refreshInBackground(ctx, root, out.Answer, fmt.Sprintf("%s query '%s' --refresh", hint.BinaryName(), input)); cause != nil {
			out.Answer.IndexCause = cause
		}
	}
	noteShardErrors(ctx, root, "magus query")

	stop := traceFromContext(ctx).phase("query.next")
	nx := newNextGate(root)
	next := nx.served(hint.NextForQuery(out))
	stop()

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate, outputName:
		return emitQueryRecord(ctx, opts, out, next)
	}
	return writeQueryText(ctx, os.Stdout, nx, next, res)
}

// writeQueryText is knowledge.WriteQueryText with next printed under the matches.
func writeQueryText(ctx context.Context, w io.Writer, nx nextGate, next []types.NextStep, res knowledge.QueryResult) error {
	return knowledge.WriteQueryText(ctx, w, func(w io.Writer) { printNext(w, nx, next) }, res)
}

// checkQuerySearch refuses a graph search whose words or flags were meant for
// something else.
func checkQuerySearch(ctx context.Context, qf *flaggen.QueryFlags, pos []string) error {
	// A graph search for an id magus printed would report `matches: 0`,
	// which reads as "that run does not exist".
	if len(pos) == 1 && cache.LooksLikeInvocationID(pos[0]) {
		slog.ErrorContext(ctx, fmt.Sprintf("%q is an invocation id, not a graph term. Read it with: %s",
			pos[0], hint.QueryInvocation.With(pos[0])), attr.Notice("magus query"))
		return exit.Reported(exit.Usage)
	}
	if qf.Open || qf.Print || qf.Attempts || qf.Identity || qf.Publish {
		// These apply only to `query output <ref>`: stop, never ignore them.
		slog.ErrorContext(ctx, fmt.Sprintf("--open/--print/--attempts/--identity/--publish apply only to `%s <ref>`. To open the knowledge graph in a browser, use `%s`.", hint.QueryOutput, hint.GraphExport.With("--open")), attr.Notice("magus query"))
		return exit.Reported(exit.Usage)
	}
	if len(pos) == 0 && qf.Kind == "" {
		slog.ErrorContext(ctx, "requires search terms", attr.Notice("magus query"))
		return exit.Reported(exit.Usage)
	}
	return nil
}

// emitQueryRecord prints out as a record or as names. A script branches on
// the status, so a blind spot must not read as an emptiness here either.
func emitQueryRecord(ctx context.Context, opts output.Options, out types.KnowledgeQueryOutput, next []types.NextStep) error {
	if err := emitQueryMatches(opts, out, next); err != nil {
		return err
	}
	// exitForQuery passes a populated answer whatever its verdict, and a short
	// list from a stale index reads as complete. stderr keeps stdout parseable.
	if err := knowledge.ReportIndexStaleness(noticeLines{ctx: ctx, level: slog.LevelError}, out.Answer); err != nil {
		return err
	}
	return knowledge.ExitForQuery(out)
}

func printQueryUsage(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, "Usage: magus query <terms> [flags]")
	fmt.Fprintln(os.Stderr, "       magus query output <ref> [-o <format>] [--open] [--attempts] [--identity] [--publish]")
	fmt.Fprintln(os.Stderr, "       magus query output --stdin")
	fmt.Fprintln(os.Stderr, "       magus query invocation <id> [-o json] [--secrets]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, types.KnowledgeQueryDefinition)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Terms are free text plus field matchers: kind=spell, project=pkg/foo,")
	fmt.Fprintln(os.Stderr, "relation=uses, id=build. Negate with != (kind!=op) and match a regex")
	fmt.Fprintln(os.Stderr, "with =~ (id=~build$). The : grammar (kind:spell, -kind:op) still works.")
	fmt.Fprintln(os.Stderr, "  magus query kind=spell go")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Field matchers filter; free text ranks. A match needs any one of the")
	fmt.Fprintln(os.Stderr, "terms, ranked by BM25F over names, signatures, paths and docs, and one")
	fmt.Fprintln(os.Stderr, "holding fewer terms than the query is a partial match. -o json says why")
	fmt.Fprintln(os.Stderr, "each match ranked (terms held, lists, weights digest). A term holding a")
	fmt.Fprintln(os.Stderr, "path separator must appear whole, and explain resolves only full matches.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "`%s <ref>` retrieves one target run's captured output by its\n", hint.QueryOutput.Leaf())
	fmt.Fprintln(os.Stderr, "output ref (out1a2b3c), shown when the target ran:")
	fmt.Fprintf(os.Stderr, "  %-38s print the exact bytes (pipe anywhere)\n", hint.QueryOutput.With("out1a2b3c"))
	fmt.Fprintf(os.Stderr, "  %-38s the descriptor + output as a record\n", hint.QueryOutput.With("out1a2b3c", "-o json"))
	fmt.Fprintf(os.Stderr, "  %-38s open it in the browser log viewer\n", hint.QueryOutput.With("out1a2b3c", "--open"))
	fmt.Fprintf(os.Stderr, "  %-38s list the ref's stored attempts\n", hint.QueryOutput.With("out1a2b3c", "--attempts"))
	fmt.Fprintf(os.Stderr, "  %-38s the run's identity + cache-key digests\n", hint.QueryOutput.With("out1a2b3c", "--identity"))
	fmt.Fprintf(os.Stderr, "  %-38s print records from -o jsonl; writes nothing\n", hint.QueryOutput.With("--stdin"))
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "`%s <id>` reads one run's journal back by the id shown as\n", hint.QueryInvocation.Leaf())
	fmt.Fprintf(os.Stderr, "`inv:` in %s:\n", hint.QueryOutput.With("<ref>", "--identity"))
	fmt.Fprintf(os.Stderr, "  %-38s the run's events, newest last\n", hint.QueryInvocation.With("invmsm3vcou1"))
	fmt.Fprintf(os.Stderr, "  %-38s only the credential reads (audit)\n", hint.QueryInvocation.With("invmsm3vcou1", "--secrets"))
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "--open respects the BROWSER environment variable to pick the browser")
	fmt.Fprintln(os.Stderr, "(e.g. BROWSER=firefox); otherwise it uses your desktop's default handler.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
	printOwnDefaults(fs)
}

// readQueryLocally prefers the stored graph while an index
// is behind the tree, then a loaded graph.
func readQueryLocally(ctx context.Context, root string, qf *flaggen.QueryFlags, read knowledge.GraphRead) (knowledge.QueryResult, error) {
	ws, err := openForRead(ctx, root)
	if err != nil {
		return knowledge.QueryResult{}, err
	}
	if !qf.Refresh && !qf.Global {
		if stored, answered := searchStoredIfStale(ctx, ws, globalCfg, read); answered {
			return stored, nil
		}
	}
	return searchGraph(ctx, ws, globalCfg, read, qf.Refresh, qf.Global)
}

func emitQueryMatches(opts output.Options, out types.KnowledgeQueryOutput, next []types.NextStep) error {
	if opts.Format == outputName {
		return emitItemNames(os.Stdout, out.Matches, func(m types.KnowledgeMatch) string { return m.ID })
	}
	return emitFormatted(opts, queryWithNext{KnowledgeQueryOutput: out, Next: next})
}

func explainCommand(ctx context.Context, root string, args []string) error {
	var xf *flaggen.ExplainFlags
	var flags *flag.FlagSet
	pos, err := commandParse("explain", args, func(fs *flag.FlagSet) {
		flags = fs
		xf = flaggen.BindExplain(fs)
		fs.Usage = func() { printExplainUsage(fs) }
	})
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		slog.ErrorContext(ctx, "requires a node ID or name", attr.Notice("magus explain"))
		return exit.Reported(exit.Usage)
	}
	if err := checkSourcePage("explain", xf.MaxLines, xf.SkipLines); err != nil {
		return err
	}
	source := explainSourceWindow(xf, flags, pos[0])
	if xf.To != "" {
		if source.Show {
			slog.ErrorContext(ctx, "--to prints a chain of edges, which has no source lines; drop --source, --max-lines and --skip-lines", attr.Notice("magus explain"))
			return exit.Reported(exit.Usage)
		}
		return explainChain(ctx, root, xf, pos[0], xf.To)
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	read := knowledge.GraphRead{Input: pos[0]}
	if xf.Refresh && !xf.Global && refreshIndexesNow(ctx, root, "magus explain") {
		return nil
	}
	res, err := readExplainResult(ctx, root, xf, read)
	if err != nil {
		return err
	}
	if !res.Found {
		res.Answer = explainMissAnswer(ctx, root, pos[0], res.Answer)
	}
	// A stale index only: reading a card never starts a first build.
	if !xf.Refresh && !xf.Global && len(res.Answer.StaleIndexes) > 0 {
		noteExplainRefresh(ctx, root, pos[0], &res)
	}
	noteShardErrors(ctx, root, "magus explain")
	if !res.Found {
		return reportExplainMiss(ctx, pos[0], res)
	}
	return emitExplainCard(ctx, root, pos[0], opts, source, res)
}

// emitExplainCard writes the card res found for input, with its next steps.
// A source window asks for the doc section's lines instead.
func emitExplainCard(ctx context.Context, root, input string, opts output.Options, source sourceWindow, res knowledge.ExplainResult) error {
	out := res.Out
	stop := traceFromContext(ctx).phase("explain.next")
	nx := newNextGate(root)
	next := nx.served(hint.NextForExplain(out))
	stop()

	var section *types.KnowledgeDefinitionSite
	if source.Show {
		site, err := docSectionSource(resolveRootOrEmpty(root), out.Node, source)
		if err != nil {
			return err
		}
		section = &site
		if opts.Format == outputText {
			return emitDocSectionSource(os.Stdout, site)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		record := explainWithNext{KnowledgeExplainOutput: out, Next: next, AgentSessions: sessionContact(root, out.Node), Source: section}
		return emitExplainRecord(opts, record, section)
	case outputName:
		return emitNames(os.Stdout, []string{out.Node.ID})
	}

	return writeExplainText(os.Stdout, root, input, nx, next, res)
}

func noteExplainRefresh(ctx context.Context, root, input string, res *knowledge.ExplainResult) {
	wait := fmt.Sprintf("%s explain '%s' --refresh", hint.BinaryName(), strings.ReplaceAll(input, "'", `'\''`))
	if cause := refreshInBackground(ctx, root, res.Answer, wait); cause != nil {
		res.Answer.IndexCause = cause
	}
}

func reportExplainMiss(ctx context.Context, input string, res knowledge.ExplainResult) error {
	slog.ErrorContext(ctx, fmt.Sprintf("no node matches %q", input), attr.Notice("magus explain"))
	missLines := noticeLines{ctx: ctx, level: slog.LevelError}
	knowledge.PrintVerdict(missLines, res.Answer, hint.Refs.With(input))
	knowledge.EmitNearest(ctx, res.Nearest)
	return knowledge.ExitForVerdict(res.Answer.Verdict)
}

func emitExplainRecord(opts output.Options, record explainWithNext, section *types.KnowledgeDefinitionSite) error {
	if err := emitFormatted(opts, record); err != nil {
		return err
	}
	if section != nil {
		return docSectionExit(*section)
	}
	return nil
}

func explainSourceWindow(xf *flaggen.ExplainFlags, flags *flag.FlagSet, subject string) sourceWindow {
	return sourceWindow{
		SourceWindow: knowledge.SourceWindow{
			Show:     xf.Source || flagGiven(flags, flaggen.FlagExplainMaxLines) || flagGiven(flags, flaggen.FlagExplainSkipLines),
			MaxLines: xf.MaxLines, SkipLines: xf.SkipLines,
		},
		again: commandAgain(hint.Explain.With, flags, subject, flaggen.FlagExplainMaxLines, flaggen.FlagExplainSkipLines),
	}
}

func readExplainResult(ctx context.Context, root string, xf *flaggen.ExplainFlags, read knowledge.GraphRead) (knowledge.ExplainResult, error) {
	var res knowledge.ExplainResult
	if !xf.Refresh && !xf.Global && askServer(ctx, root, readExplain, &read, &res) {
		return res, nil
	}
	ws, err := openForRead(ctx, root)
	if err != nil {
		return knowledge.ExplainResult{}, err
	}
	return explainNode(ctx, ws, globalCfg, read, xf.Refresh, xf.Global)
}

func printExplainUsage(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, "Usage: magus explain <node-id-or-name> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, types.KnowledgeExplainDefinition)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The argument is a node ID (target:pkg/foo:build) or a name that resolves")
	fmt.Fprintln(os.Stderr, "to one (build). Flags (global flags also accepted, see `magus -h`):")
	printOwnDefaults(fs)
}

// explainMissAnswer drops each gap no build closes whose project
// holds no text of the node's name, as refs does.
func explainMissAnswer(ctx context.Context, root, input string, ans types.KnowledgeAnswer) types.KnowledgeAnswer {
	searchRoot := resolveRootOrEmpty(root)
	if searchRoot == "" || !slices.ContainsFunc(ans.Gaps, func(g types.KnowledgeSymbolGap) bool { return g.Unindexable }) {
		return ans
	}
	name := input
	if _, rest, kinded := strings.Cut(input, ":"); kinded {
		name = rest
	}
	matches, _, _, _, _, err := textScan(ctx, searchRoot, name, nil, false, nil)
	if err != nil {
		return ans
	}
	return knowledge.WithoutUnheldGaps(ans, searchRoot, textHits(matches))
}

// writeExplainText writes res, the node asked resolved to, as `magus explain` prints it
// in text form, with next under it, and returns the error that sets the exit status.
// res must be found.
func writeExplainText(w io.Writer, root, asked string, nx nextGate, next []types.NextStep, res knowledge.ExplainResult) error {
	out := res.Out
	fmt.Fprint(w, knowledge.ResolutionNote(asked, out))
	fmt.Fprint(w, render.ExplainText(out))
	printSessionContact(w, root, out.Node)

	// Complementary deep-link: focus this node in the live Graph Explorer with a
	// blast view (the console's own analogue of `magus explain`). Symbol nodes are
	// excluded from the live full graph the explorer loads, so a link to one would
	// open to an empty focus; omit it. The link is always printed for other kinds;
	// the server may not be up when the browser opens it, hence the hint.
	if out.Node.Kind != types.KindSymbol {
		link := liveExplorerLink(graphLinkOpts{View: "blast", Node: out.Node.ID})
		fmt.Fprintf(w, "\nView in Graph Explorer: %s\n", link)
		fmt.Fprintf(w, "%s\n", authHint(link))
		fmt.Fprintf(w, "(start the magus server if the graph does not load)\n")
	}
	printNext(w, nx, next)
	return knowledge.ReportIndexStaleness(w, res.Answer)
}

// explainChain is `magus explain <from> --to <to>`: the shortest chain of edges
// between the two nodes instead of from's card.
func explainChain(ctx context.Context, root string, xf *flaggen.ExplainFlags, from, to string) error {
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	ws, err := openForRead(ctx, root)
	if err != nil {
		return err
	}
	g, err := loadWorkspaceGraph(ctx, ws, globalCfg, xf.Refresh, xf.Global, knowledge.SeedsLazyLayer(from) || knowledge.SeedsLazyLayer(to))
	if err != nil {
		return err
	}
	out, ok := g.Path(from, to)
	if !ok {
		slog.ErrorContext(ctx, fmt.Sprintf("could not resolve %q or %q to a node", from, to), attr.Notice("magus explain --to"))
		return exit.Reported(exit.Usage)
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		names := make([]string, 0, len(out.Steps)+1)
		for i, step := range out.Steps {
			if i == 0 {
				names = append(names, step.From)
			}
			names = append(names, step.To)
		}
		return emitNames(os.Stdout, names)
	}

	fmt.Print(render.PathText(out))
	return nil
}

// A running server answers query, explain and refs for the CLI. It holds the workspace
// open with its process warm, so the read skips process start, the workspace load and the
// tool probes. It still runs the same cache-first build a local read runs, whose stamps
// decide freshness against the tree as it is at that moment; nothing is trusted to a file
// watcher, so the answer is the one a local read would give. A server answers only for the
// client's exact build (proc refuses another) and only when the client reads under the
// configuration the server reads under (serveGraphRead declines otherwise). No server, a
// refusal and a broken socket all fall back to the local read.
const (
	readQuery   = "query"
	readExplain = "explain"
	readRefs    = "refs"
)

func searchGraph(ctx context.Context, ws graphWorkspace, cfg config.Config, read knowledge.GraphRead, refresh, global bool) (knowledge.QueryResult, error) {
	tr := traceFromContext(ctx)
	seeded := knowledge.SeedsLazyLayer(read.Input)
	// A reader context the VCS cannot give ranks without it, and the ranking record says so:
	// a shallow clone with no merge base still answers the query.
	stop := tr.phase("query.reader_context")
	reader, readerErr := workspacegraph.ReaderContext(ctx, ws, ws.Root())
	stop()
	ctx = knowledge.WithTable(knowledge.WithReader(ctx, reader), embedtable.Cached)
	var g *knowledge.Graph
	var out types.KnowledgeQueryOutput
	var err error
	if seeded && !global {
		// The answer loadWorkspaceGraph then Query give, ranked from the symbol names index so only
		// the shards the answer touches are decoded.
		stop := tr.phase("query.load_and_search")
		if refresh {
			seedFromPublishedGraph(ctx, ws)
		}
		out, g, err = workspacegraph.Query(ctx, ws, ws.Root(), cfg, refresh, read.Input, read.Budget, slog.Default())
		stop()
		if err != nil {
			return knowledge.QueryResult{}, err
		}
	} else {
		stop := tr.phase("query.load_graph")
		g, err = loadWorkspaceGraph(ctx, ws, cfg, refresh, global, seeded)
		stop()
		if err != nil {
			return knowledge.QueryResult{}, err
		}
		stop = tr.phase("query.search")
		out, err = g.QueryRanked(read.Input, read.Budget, 0, 0, knowledge.RetrieveOptions{Reader: reader, Weights: knowledge.ShippedWeights(), Table: embedtable.Cached})
		stop()
		if err != nil {
			return knowledge.QueryResult{}, err
		}
	}
	if readerErr != nil && out.Ranking != nil {
		out.Ranking.Skipped = append(out.Ranking.Skipped, "reader: "+readerErr.Error())
	}
	return answerQuery(ctx, g, read, out, seeded, measuredCoverage(ctx, ws, cfg)), nil
}

// coverageFunc says what the symbol layer covers for a read of input, as
// measureSymbolCoverage does.
type coverageFunc func(input string, seeded bool) knowledge.Coverage

// measuredCoverage measures ws under cfg afresh on every call, as a single read does.
func measuredCoverage(ctx context.Context, ws graphWorkspace, cfg config.Config) coverageFunc {
	return func(input string, seeded bool) knowledge.Coverage {
		return measureSymbolCoverage(ctx, ws, cfg, input, seeded)
	}
}

// searchLoaded is searchGraph over g, a graph loaded once for many reads: it must hold
// every symbol shard when read seeds the symbol layer, and only the default graph
// otherwise. table loads the embed table, nil to rank without it; searchGraph passes
// embedtable.Cached.
func searchLoaded(ctx context.Context, g *knowledge.Graph, read knowledge.GraphRead, table func() (*embed.Table, error), coverage coverageFunc) knowledge.QueryResult {
	stop := traceFromContext(ctx).phase("query.search")
	out := g.QueryPage(read.Input, read.Budget, 0, 0, table)
	stop()
	return answerQuery(ctx, g, read, out, knowledge.SeedsLazyLayer(read.Input), coverage)
}

// answerQuery judges out, what g matched for read, and names the nearest node when
// nothing matched.
func answerQuery(ctx context.Context, g *knowledge.Graph, read knowledge.GraphRead, out types.KnowledgeQueryOutput, seeded bool, coverage coverageFunc) knowledge.QueryResult {
	stop := traceFromContext(ctx).phase("query.coverage")
	out.Answer = knowledge.Answer(read.Input, out.MatchCount > 0, coverage(read.Input, seeded))
	stop()
	res := knowledge.QueryResult{Out: out}
	if out.MatchCount == 0 && read.Nearest {
		res.Nearest = g.NearestNode(read.Input)
	}
	return res
}

func explainNode(ctx context.Context, ws graphWorkspace, cfg config.Config, read knowledge.GraphRead, refresh, global bool) (knowledge.ExplainResult, error) {
	tr := traceFromContext(ctx)
	seeded := knowledge.SeedsLazyLayer(read.Input)
	stop := tr.phase("explain.load_graph")
	g, err := loadWorkspaceGraph(ctx, ws, cfg, refresh, global, seeded)
	stop()
	if err != nil {
		return knowledge.ExplainResult{}, err
	}
	return explainLoaded(ctx, g, read, measuredCoverage(ctx, ws, cfg)), nil
}

// explainLoaded is explainNode over g, a graph loaded once for many reads: it must hold
// every symbol shard when read seeds the symbol layer, and only the default graph
// otherwise.
func explainLoaded(ctx context.Context, g *knowledge.Graph, read knowledge.GraphRead, coverage coverageFunc) knowledge.ExplainResult {
	tr := traceFromContext(ctx)
	seeded := knowledge.SeedsLazyLayer(read.Input)
	stop := tr.phase("explain.search")
	out, found := g.Explain(read.Input)
	stop()
	// A bare name does not seed symbols, so a miss was resolved against a graph that held
	// no code symbols. Reporting that as "no node matches" is how a real symbol comes to
	// look nonexistent, so the answer says so when the input could have named one, and a
	// typo'd `kind:target` still gets the absent verdict it deserves.
	stop = tr.phase("explain.coverage")
	cov := coverage(read.Input, seeded)
	if found {
		// One symbol: only its own language's indexes could have missed a site of it.
		cov = cov.Narrow(g.SymbolLanguage(read.Input))
	}
	res := knowledge.ExplainResult{Out: out, Found: found, Answer: knowledge.Answer(read.Input, found, cov)}
	stop()
	if !found {
		res.Nearest = g.NearestNode(read.Input)
	}
	return res
}

// graphWorkspace is what a graph read needs of its workspace: the root, and the Inspector
// facts a rebuild reads. The *magus.Magus a server holds satisfies it, and so does the
// *magus.LazyWorkspace a CLI read opens, which evaluates the workspace only if the stored
// graph turns out stale.
type graphWorkspace interface {
	types.Inspector
	Root() string
}

// openForRead opens the workspace for a graph read no server answered, lazily: the read
// gets a handle that evaluates the workspace on the first call that needs the model, and
// a read the stored graph answers makes none. Measured 2026-10-02 on this repository, the
// eager open was 425ms of a warm `magus query`'s 590ms. When startup already opened the
// workspace, that handle answers, as it always did.
//
// The open, when it comes, is the one openWorkspaceForRead makes: the cache-backed handle
// whose remote shard backing a rebuild pushes to.
func openForRead(ctx context.Context, root string) (graphWorkspace, error) { //nolint:ireturn // the workspace backing is chosen at runtime
	stop := traceFromContext(ctx).phase("read.open")
	defer stop()
	if m, ok := loadedMagus(); ok {
		return m, nil
	}
	wsRoot, err := workspace.FindRoot(root)
	if err != nil {
		return nil, err
	}
	return magus.NewLazyWorkspace(wsRoot, func(ctx context.Context) (*magus.Magus, error) {
		ws, err := openWorkspaceForRead(ctx, root)
		if err != nil {
			return nil, err
		}
		return asMagus(ws)
	}), nil
}

// openWorkspaceForRead opens the workspace eagerly for a read that needs the whole
// set of workspace operations (refs, which reads symbols and classifies files). It is the same
// Open-loaded handle startup used to preload for these verbs, so the local read is the one
// it always was; the preload just no longer runs for a read a server answers.
func openWorkspaceForRead(ctx context.Context, root string) (types.WorkspaceRepository, error) { //nolint:ireturn // the workspace backing is chosen at runtime
	// inspectWorkspace reuses this handle when the open succeeded, and reports why when it
	// did not, so the error has one place to come from.
	_, _ = loadMagus(ctx, root)
	return inspectWorkspace(ctx, root)
}

// asMagus is the *magus.Magus behind a workspace handle. Open and Inspect both return one,
// so anything else is a programming error worth naming rather than a nil to chase.
func asMagus(ws types.WorkspaceRepository) (*magus.Magus, error) {
	m, ok := ws.(*magus.Magus)
	if !ok {
		return nil, fmt.Errorf("the workspace handle is a %T, not a *magus.Magus", ws)
	}
	return m, nil
}

// fullWorkspace is the whole set of workspace operations behind a graph workspace, opening a lazy
// one: for the reads that need more than the Inspector (the global graph unions
// WorkspaceRepository handles).
func fullWorkspace(ctx context.Context, ws graphWorkspace) (types.WorkspaceRepository, error) { //nolint:ireturn // the workspace backing is chosen at runtime
	switch w := ws.(type) {
	case types.WorkspaceRepository:
		return w, nil
	case *magus.LazyWorkspace:
		return w.Magus(ctx)
	}
	return nil, fmt.Errorf("%T is not a workspace", ws)
}

// searchStoredIfStale answers read from the stored graph when an index is
// behind the tree, false otherwise. An edit moves the graph's stamps too, and
// reassembling it costs a read 10-30 s the background refresh pays.
func searchStoredIfStale(ctx context.Context, ws graphWorkspace, cfg config.Config, read knowledge.GraphRead) (knowledge.QueryResult, bool) {
	if !knowledge.CouldMatchLazyLayer(read.Input) {
		return knowledge.QueryResult{}, false
	}
	stale := findStaleIndexes(ctx, ws)
	if len(stale) == 0 {
		return knowledge.QueryResult{}, false
	}
	cacheDir, err := magus.ResolveCacheDir(ws.Root(), workspace.WithLoadedConfig(cfg))
	if err != nil {
		return knowledge.QueryResult{}, false
	}
	store := knowledge.NewStore(cacheDir, true, 0, nil, slog.Default())
	g, err := store.Load(ctx)
	if err != nil {
		return knowledge.QueryResult{}, false
	}
	g.SetRoot(ws.Root())
	if knowledge.SeedsLazyLayer(read.Input) {
		if err := store.MergeSymbolShards(ctx, g); err != nil || !g.HasSymbols() {
			return knowledge.QueryResult{}, false
		}
	}
	return searchLoaded(ctx, g, read, embedtable.Cached, knownStaleCoverage(ctx, ws, cfg, stale)), true
}

// detachedRefreshEnv marks the process startRefresh detached.
const detachedRefreshEnv = "MAGUS_DETACHED_REFRESH"

// refreshNotices carries a refresh's lock progress to the notice channel under verb.
func refreshNotices(ctx context.Context, verb string) io.Writer {
	return noticeLines{ctx: ctx, level: slog.LevelInfo, label: verb, prefix: "magus graph build: "}
}

// refreshIndexesNow is `--refresh`: it waits for a refresh already running,
// brings the stale indexes up to the tree, and says on stderr what that cost.
// What stays behind it names at error level, and the read still answers.
// It returns true when the command should exit instead: a detached refresh
// has no reader, and its read would hold a graph outside these locks.
func refreshIndexesNow(ctx context.Context, root, verb string) bool {
	detached := os.Getenv(detachedRefreshEnv) != ""
	_ = os.Unsetenv(detachedRefreshEnv)
	ctx, cancel := context.WithTimeout(ctx, magus.SymbolRefreshBudget)
	defer cancel()
	retry := fmt.Sprintf("`%s` retries", hint.GraphBuild)
	failed := func(err error) bool {
		slog.ErrorContext(ctx, "could not refresh the symbol indexes; "+retry, attr.Notice(verb), attr.Error(err))
		return detached
	}
	wsRoot := resolveRootOrEmpty(root)
	if wsRoot == "" {
		return failed(errors.New("no workspace root here, so no knowledge store to refresh"))
	}
	storeDir, err := knowledgeStoreDir(wsRoot)
	if err != nil {
		return failed(err)
	}
	me := maintenance.GraphBuildHolder{PID: os.Getpid(), Started: time.Now(), By: "the symbol refresh of " + wsRoot, Store: storeDir}
	machine, err := maintenance.TakeRefreshLock(ctx, maintenance.MachineRefreshLock(), me, verb, detached, refreshNotices(ctx, verb), graphBuildPoll)
	if err != nil {
		return failed(err)
	}
	if machine == nil {
		return true
	}
	defer func() { _ = machine.Release() }()
	store, err := maintenance.TakeRefreshLock(ctx, maintenance.StoreRefreshLock(storeDir), me, verb, detached, refreshNotices(ctx, verb), graphBuildPoll)
	if err != nil {
		return failed(err)
	}
	if store == nil {
		return true
	}
	defer func() { _ = store.Release() }()
	m, err := loadMagus(ctx, root)
	if err != nil {
		return failed(err)
	}
	refresh := m.RefreshSymbolIndexes(ctx)
	spent := func(d time.Duration) time.Duration { return d.Round(100 * time.Millisecond) }
	if n := len(refresh.Refreshed); n > 0 {
		slog.InfoContext(ctx, fmt.Sprintf("refreshed %d symbol index(es) in %s (finding them %s, indexers %s, guard indexes %s): %s", n, spent(refresh.Took),
			spent(refresh.Swept), spent(refresh.Indexed), spent(refresh.Took-refresh.Swept-refresh.Indexed), strings.Join(refresh.Refreshed, ", ")), attr.Notice(verb))
	}
	if len(refresh.Problems) > 0 {
		slog.ErrorContext(ctx, fmt.Sprintf("could not refresh every symbol index in %s, so this answer is drawn from them as they stand; %s: %s",
			spent(refresh.Took), retry, strings.Join(refresh.Problems, "; ")), attr.Notice(verb))
	}
	return detached
}

// refreshInBackground starts a refresh of the indexes behind ans and answers
// what the read should say about it, nil when it has nothing to say. A plain
// read never waits on an indexer. wait is the command that does.
func refreshInBackground(ctx context.Context, root string, ans types.KnowledgeAnswer, wait string) *types.KnowledgeIndexCause {
	if !ans.IndexBehind() || globalCfg.DryRun || globalCfg.Knowledge.SymbolIndexing.Disabled {
		return nil
	}
	dir, err := knowledgeStoreDir(root)
	if err != nil {
		return nil
	}
	fix := fmt.Sprintf("ask again once it lands, or wait for it: `%s`", wait)
	if holder, running := maintenance.RunningGraphBuild(dir); running {
		return &types.KnowledgeIndexCause{Why: fmt.Sprintf("this answer comes from the index as it stands, while pid %d refreshes it", holder.PID), Fix: fix}
	}
	if failed := magus.RecentSymbolRefreshFailures(filepath.Dir(dir), time.Now()); len(failed) > 0 {
		why := "its last refresh failed: " + strings.Join(failed, "; ")
		slog.ErrorContext(ctx, why, attr.Notice("magus"))
		return &types.KnowledgeIndexCause{Why: why, Fix: fmt.Sprintf("`%s`", hint.GraphBuild)}
	}
	holder, running, err := maintenance.RunningMachineGraphBuild()
	if err != nil {
		slog.ErrorContext(ctx, "could not start a background refresh", attr.Notice("magus"), attr.Error(err))
		return &types.KnowledgeIndexCause{Why: "this answer comes from the index as it stands, and a background refresh would not start: " + err.Error(), Fix: fmt.Sprintf("`%s`", hint.GraphBuild)}
	}
	if running {
		return &types.KnowledgeIndexCause{Why: fmt.Sprintf("this answer comes from the index as it stands, and this read started no refresh: a machine runs one at a time, and %s is running", holder), Fix: fix}
	}
	started, err := startRefresh(dir)
	switch {
	case err != nil:
		slog.ErrorContext(ctx, "could not start a background refresh", attr.Notice("magus"), attr.Error(err))
		return &types.KnowledgeIndexCause{Why: "this answer comes from the index as it stands, and a background refresh would not start: " + err.Error(), Fix: fmt.Sprintf("`%s`", hint.GraphBuild)}
	case !started:
		return nil
	}
	return &types.KnowledgeIndexCause{Why: "this answer comes from the index as it stands, and a refresh started in the background", Fix: fix}
}

// startRefresh runs this command again with --refresh, detached, logging to
// a file in dir. A test binary starts nothing: it would rerun its tests.
func startRefresh(dir string) (bool, error) {
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	if strings.HasSuffix(exe, ".test") {
		return false, nil
	}
	logFile, err := createRefreshLog(dir)
	if err != nil {
		return false, err
	}
	defer logFile.Close()
	return launchRefresh(refreshCommand(exe, logFile))
}

func launchRefresh(cmd *exec.Cmd) (bool, error) {
	if err := cmd.Start(); err != nil {
		return false, err
	}
	return true, cmd.Process.Release()
}

func refreshCommand(exe string, logFile *os.File) *exec.Cmd {
	cmd := detachedCommand(exe, append(slices.Clone(os.Args[1:]), "--refresh"))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = append(os.Environ(), detachedRefreshEnv+"=1")
	cmd.SysProcAttr = detachedSysProcAttr()
	return cmd
}

func createRefreshLog(dir string) (*os.File, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return os.Create(filepath.Join(dir, "symbol-refresh.log"))
}

// serveGraphRead answers one CLI graph read for m, a workspace the server holds, reading
// under cfg, the configuration the server resolves for m's root now.
func serveGraphRead(ctx context.Context, m *magus.Magus, cfg config.Config, verb string, request []byte) ([]byte, error) {
	var read knowledge.GraphRead
	if err := json.Unmarshal(request, &read); err != nil {
		return nil, fmt.Errorf("magus server: decode the %s read: %w", verb, err)
	}
	if digest := config.ReadDigest(cfg); digest == "" || read.Config != digest {
		return nil, fmt.Errorf("%w: the client reads under another configuration", proc.ErrNotAdoptable)
	}
	// m was opened under the configuration its root had then. The cache directory is the
	// setting m itself reads from that copy, so one moved since means a workspace the
	// server must reopen before it answers for it.
	if dir, err := magus.ResolveCacheDir(m.Root(), workspace.WithLoadedConfig(cfg)); err != nil || dir != m.CacheDir() {
		return nil, fmt.Errorf("%w: the workspace was opened under an older configuration", proc.ErrNotAdoptable)
	}
	for _, name := range m.SymbolFreshnessEnv() {
		v, set := os.LookupEnv(name)
		got, sent := read.Env[name]
		if set != sent || (set && got != config.EnvDigest(v)) {
			return nil, fmt.Errorf("%w: the client's %s differs from the server's", proc.ErrNotAdoptable, name)
		}
	}
	var res any
	var err error
	switch verb {
	case readQuery:
		res, err = searchGraph(ctx, m, cfg, read, false, false)
	case readExplain:
		res, err = explainNode(ctx, m, cfg, read, false, false)
	case readRefs:
		var g *knowledge.Graph
		if g, err = loadRefsGraph(ctx, m, cfg, false, read.Input); err == nil {
			res = lookupRefs(ctx, m, cfg, g, read, measuredCoverage(ctx, m, cfg))
		}
	default:
		return nil, fmt.Errorf("%w: no %q read", proc.ErrNotAdoptable, verb)
	}
	if err != nil {
		return nil, err
	}
	return json.Marshal(res)
}

// splitCSV splits a comma-separated flag value, trimming blanks.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// askServer has a running server answer verb into reply and reports whether it did.
func askServer(ctx context.Context, root, verb string, read *knowledge.GraphRead, reply any) bool {
	defer traceFromContext(ctx).phase("read.server")()
	return server.AskGraphRead(ctx, globalCfg, version, resolveRootOrEmpty(root), verb, read, reply)
}
