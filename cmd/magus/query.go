package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
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
	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/embedtable"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/internal/server"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/textindex"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/libs/safetyscissors/journal"
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
	flags, positionals := commands.PartitionFlags(fs, args)
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
			} else if !commands.FlagIsBool(f) && i+1 < len(flags) {
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

	stop := observability.TraceFromContext(ctx).Phase("query.next")
	nx := newNextGate(ctx, root)
	next := nx.served(hint.NextForQuery(out))
	stop()

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate, outputName:
		return emitQueryRecord(ctx, opts, out, next)
	case outputText:
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
	if len(pos) == 1 && recorder.LooksLikeInvocationID(pos[0]) {
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
	return graphReads.SearchGraph(ctx, ws, globalCfg, read, qf.Refresh, qf.Global)
}

func emitQueryMatches(opts output.Options, out types.KnowledgeQueryOutput, next []types.NextStep) error {
	if opts.Format == outputName {
		return emitItemNames(os.Stdout, out.Matches, func(m types.KnowledgeMatch) string { return m.ID })
	}
	return emitFormatted(opts, job.QueryWithNext{KnowledgeQueryOutput: out, Next: next})
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
	if err := knowledge.CheckSourcePage("explain", xf.MaxLines, xf.SkipLines); err != nil {
		return err
	}
	source := sourceWindow{
		SourceWindow: knowledge.SourceWindow{
			Show:     xf.Source || flagGiven(flags, flaggen.FlagExplainMaxLines) || flagGiven(flags, flaggen.FlagExplainSkipLines),
			MaxLines: xf.MaxLines, SkipLines: xf.SkipLines,
		},
		again: commandAgain(hint.Explain.With, flags, pos[0], flaggen.FlagExplainMaxLines, flaggen.FlagExplainSkipLines),
	}
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
	stop := observability.TraceFromContext(ctx).Phase("explain.next")
	nx := newNextGate(ctx, root)
	next := nx.served(hint.NextForExplain(out))
	stop()

	var section *types.KnowledgeDefinitionSite
	if source.Show {
		site, err := knowledge.DocSectionSource(resolveRootOrEmpty(root), out.Node, source.refs())
		if err != nil {
			return err
		}
		section = &site
		if opts.Format == outputText {
			return knowledge.EmitDocSectionSource(os.Stdout, site)
		}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		record := job.ExplainWithNext{KnowledgeExplainOutput: out, Next: next, AgentSessions: sessionContact(root, out.Node), Source: section}
		return emitExplainRecord(opts, record, section)
	case outputName:
		return emitNames(os.Stdout, []string{out.Node.ID})
	case outputText:
	}

	return writeExplainText(ctx, os.Stdout, root, input, nx, next, res)
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

func emitExplainRecord(opts output.Options, record job.ExplainWithNext, section *types.KnowledgeDefinitionSite) error {
	if err := emitFormatted(opts, record); err != nil {
		return err
	}
	if section != nil {
		return knowledge.DocSectionExit(*section)
	}
	return nil
}

// sourceWindow is the window explain builds: knowledge's window and the command that asked.
type sourceWindow struct {
	knowledge.SourceWindow
	again string
}

func (w sourceWindow) refs() knowledge.RefsWindow {
	return knowledge.RefsWindow{SourceWindow: w.SourceWindow, Again: w.again}
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
	return graphReads.ExplainNode(ctx, ws, globalCfg, read, xf.Refresh, xf.Global)
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
	matches, _, _, _, _, err := textindex.Search(ctx, searchRoot, name, nil, false, nil)
	if err != nil {
		return ans
	}
	return knowledge.WithoutUnheldGaps(ans, searchRoot, knowledge.TextHits(matches))
}

// writeExplainText writes res, the node asked resolved to, as `magus explain` prints it
// in text form, with next under it, and returns the error that sets the exit status.
// res must be found.
func writeExplainText(_ context.Context, w io.Writer, root, asked string, nx nextGate, next []types.NextStep, res knowledge.ExplainResult) error {
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
		link := liveExplorerLink(console.GraphLinkOpts{View: "blast", Node: out.Node.ID})
		fmt.Fprintf(w, "\nView in Graph Explorer: %s\n", link)
		fmt.Fprintf(w, "%s\n", console.AuthHint(link))
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
	g, err := graphReads.LoadGraph(ctx, ws, globalCfg, xf.Refresh, xf.Global, knowledge.SeedsLazyLayer(from) || knowledge.SeedsLazyLayer(to))
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
	case outputText:
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

// graphReads runs the graph reads on what only this package can hand them: the symbol
// coverage probe, and the open of a lazy workspace into a whole one.
var graphReads = workspacegraph.Reads{Measure: measureSymbolCoverage, Full: fullWorkspace}

// openForRead opens the workspace for a graph read no server answered, lazily: the read
// gets a handle that evaluates the workspace on the first call that needs the model, and
// a read the stored graph answers makes none. Measured 2026-10-02 on this repository, the
// eager open was 425ms of a warm `magus query`'s 590ms. When startup already opened the
// workspace, that handle answers, as it always did.
//
// The open, when it comes, is the one openWorkspaceForRead makes: the cache-backed handle
// whose remote shard backing a rebuild pushes to.
func openForRead(ctx context.Context, root string) (workspacegraph.ReadWorkspace, error) { //nolint:ireturn // the workspace backing is chosen at runtime
	stop := observability.TraceFromContext(ctx).Phase("read.open")
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
func fullWorkspace(ctx context.Context, ws workspacegraph.ReadWorkspace) (types.WorkspaceRepository, error) { //nolint:ireturn // the workspace backing is chosen at runtime
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
func searchStoredIfStale(ctx context.Context, ws workspacegraph.ReadWorkspace, cfg config.Config, read knowledge.GraphRead) (knowledge.QueryResult, bool) {
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
	return workspacegraph.SearchLoaded(ctx, g, read, embedtable.Cached, knownStaleCoverage(ctx, ws, cfg, stale)), true
}

// detachedRefreshEnv marks the process startRefresh detached.
const detachedRefreshEnv = "MAGUS_DETACHED_REFRESH"

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
	machine, err := maintenance.TakeRefreshLock(ctx, maintenance.MachineRefreshLock(), me, verb, detached, noticeLines{ctx: ctx, level: slog.LevelInfo, label: verb, prefix: "magus graph build: "}, maintenance.GraphBuildPoll)
	if err != nil {
		return failed(err)
	}
	if machine == nil {
		return true
	}
	defer func() { _ = machine.Release() }()
	store, err := maintenance.TakeRefreshLock(ctx, maintenance.StoreRefreshLock(storeDir), me, verb, detached, noticeLines{ctx: ctx, level: slog.LevelInfo, label: verb, prefix: "magus graph build: "}, maintenance.GraphBuildPoll)
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
		return &types.KnowledgeIndexCause{Why: fmt.Sprintf("this answer comes from the index as it stands, while pid %d refreshes it", holder.PID), Hint: fix}
	}
	if failed := magus.RecentSymbolRefreshFailures(filepath.Dir(dir), time.Now()); len(failed) > 0 {
		why := "its last refresh failed: " + strings.Join(failed, "; ")
		slog.ErrorContext(ctx, why, attr.Notice("magus"))
		return &types.KnowledgeIndexCause{Why: why, Hint: fmt.Sprintf("`%s`", hint.GraphBuild)}
	}
	holder, running, err := maintenance.RunningMachineGraphBuild()
	if err != nil {
		slog.ErrorContext(ctx, "could not start a background refresh", attr.Notice("magus"), attr.Error(err))
		return &types.KnowledgeIndexCause{Why: "this answer comes from the index as it stands, and a background refresh would not start: " + err.Error(), Hint: fmt.Sprintf("`%s`", hint.GraphBuild)}
	}
	if running {
		return &types.KnowledgeIndexCause{Why: fmt.Sprintf("this answer comes from the index as it stands, and this read started no refresh: a machine runs one at a time, and %s is running", holder), Hint: fix}
	}
	started, err := startRefresh(dir)
	switch {
	case err != nil:
		slog.ErrorContext(ctx, "could not start a background refresh", attr.Notice("magus"), attr.Error(err))
		return &types.KnowledgeIndexCause{Why: "this answer comes from the index as it stands, and a background refresh would not start: " + err.Error(), Hint: fmt.Sprintf("`%s`", hint.GraphBuild)}
	case !started:
		return nil
	}
	return &types.KnowledgeIndexCause{Why: "this answer comes from the index as it stands, and a refresh started in the background", Hint: fix}
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
	cmd := proc.DetachedCommand(exe, append(slices.Clone(os.Args[1:]), "--refresh"))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.Env = append(os.Environ(), detachedRefreshEnv+"=1")
	cmd.SysProcAttr = proc.DetachedSysProcAttr()
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
	if err := codec.Unmarshal(request, &read); err != nil {
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
		res, err = graphReads.SearchGraph(ctx, m, cfg, read, false, false)
	case readExplain:
		res, err = graphReads.ExplainNode(ctx, m, cfg, read, false, false)
	case readRefs:
		var g *knowledge.Graph
		if g, err = loadRefsGraph(ctx, m, cfg, false, read.Input); err == nil {
			res = lookupRefs(ctx, m, cfg, g, read, graphReads.MeasuredCoverage(ctx, m, cfg))
		}
	default:
		return nil, fmt.Errorf("%w: no %q read", proc.ErrNotAdoptable, verb)
	}
	if err != nil {
		return nil, err
	}
	return codec.Marshal(res)
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
	defer observability.TraceFromContext(ctx).Phase("read.server")()
	return server.AskGraphRead(ctx, globalCfg, version, resolveRootOrEmpty(root), verb, read, reply)
}

// queryOutputStdin is `magus query output --stdin`.
func queryOutputStdin(ctx context.Context, qf *flaggen.QueryFlags, pos []string) error {
	if len(pos) != 1 || pos[0] != hint.QueryOutput.Leaf() {
		return exit.Usagef("magus query: --stdin applies only to `%s --stdin`, which takes no ref", hint.QueryOutput)
	}
	if qf.Attempts || qf.Identity || qf.Publish || qf.Open || qf.Print {
		// A record on stdin is untrusted: printed, never published or opened.
		return exit.Usagef("magus query output --stdin: --attempts, --identity, --publish, --open and --print act on a stored run; a record on stdin is only printed")
	}
	outOpts, oerr := outputOptionsOrDefault()
	if oerr != nil {
		return oerr
	}
	if outOpts.Format != outputText {
		return exit.Usagef("magus query output --stdin takes no -o: it prints each untrusted record neutralized, and the records on stdin are already the structured form")
	}
	return cache.PrintOutputRecords(os.Stdin, os.Stdout, noticeLines{ctx: ctx, level: slog.LevelInfo})
}

// queryOutputCommand is `magus query output <ref>`; pos follows `query`.
func queryOutputCommand(ctx context.Context, root string, qf *flaggen.QueryFlags, pos []string) error {
	if len(pos) != 2 {
		slog.ErrorContext(ctx, fmt.Sprintf("expected exactly one ref (e.g. %s)", hint.QueryOutput.With("out1a2b3c")), attr.Notice(hint.QueryOutput.String()))
		return exit.Reported(exit.Usage)
	}
	ref := pos[1]
	if !cache.LooksLikeRef(ref) {
		if !journal.ValidPayloadRef(ref) {
			msg := fmt.Sprintf("%q is not an output ref (expected out<hex> for a run, or a stored payload such as grd<hex> or mcp<hex>)", ref)
			slog.ErrorContext(ctx, "", attr.Notice("magus query output"), attr.Error(types.DiagnosticErrorf(types.OutputRefMalformed, "%s", msg)))
			return exit.Reported(exit.Usage)
		}
		if qf.Attempts || qf.Identity || qf.Publish || qf.Open || qf.Print {
			return exit.Usagef("magus query output: %s is a stored payload, not a run; --attempts, --identity, --publish, --open and --print apply only to out<hex> refs", ref)
		}
		outOpts, oerr := outputOptionsOrDefault()
		if oerr != nil {
			return oerr
		}
		return queryTrailPayload(ctx, root, ref, outOpts)
	}
	outOpts, oerr := outputOptionsOrDefault()
	if oerr != nil {
		return oerr
	}
	exclusive := 0
	for _, set := range []bool{qf.Attempts, qf.Identity, qf.Publish, qf.Open || qf.Print} {
		if set {
			exclusive++
		}
	}
	if exclusive > 1 {
		slog.ErrorContext(ctx, "--attempts, --identity, --publish, and --open/--print are distinct actions; pick one", attr.Notice("magus query output"))
		return exit.Reported(exit.Usage)
	}
	return queryOutputRef(ctx, root, ref, outputRefOpts{open: qf.Open, printURL: qf.Print, viewerBase: qf.URL, attempts: qf.Attempts, identity: qf.Identity, publish: qf.Publish, out: outOpts})
}

// queryInvocationCommand is `magus query invocation <id>`; pos follows `query`.
func queryInvocationCommand(ctx context.Context, root string, qf *flaggen.QueryFlags, pos []string) error {
	if len(pos) != 2 {
		slog.ErrorContext(ctx, fmt.Sprintf("expected exactly one invocation id (e.g. %s)", hint.QueryInvocation.With("invmsm3vcou1")),
			attr.Notice(hint.QueryInvocation.String()))
		return exit.Reported(exit.Usage)
	}
	inv := pos[1]
	if !recorder.LooksLikeInvocationID(inv) {
		slog.ErrorContext(ctx, fmt.Sprintf("%q is not an invocation id (expected inv<id>, e.g. invmsm3vcou1); a run prints one as `inv:` in %s",
			inv, hint.QueryOutput.With("<ref> --identity")), attr.Notice("magus query invocation"))
		return exit.Reported(exit.Usage)
	}
	outOpts, oerr := outputOptionsOrDefault()
	if oerr != nil {
		return oerr
	}
	return queryInvocation(ctx, root, inv, qf.Secrets, outOpts)
}

// outputRefOpts carries the options for `magus query output <ref>`.
type outputRefOpts struct {
	open       bool           // open the browser log viewer instead of printing
	printURL   bool           // with open, print the URL instead of launching a browser
	viewerBase string         // log viewer base URL
	attempts   bool           // list the ref's stored executions instead of printing output
	identity   bool           // show the run's identity (descriptor, lineage, key digests)
	publish    bool           // upload this run's output to the remote as a signed bundle
	out        output.Options // -o: text prints raw bytes, json/yaml prints the descriptor record
}

// queryOutputRef retrieves a target's captured output by reference id (or unique prefix). The
// default prints the exact bytes to stdout (pipe-friendly); -o json/yaml prints the descriptor
// record; --open hands it to the browser log viewer. The bytes never leave the machine: --open
// rides them in a URL fragment, exactly like `magus graph export --open`.
func queryOutputRef(ctx context.Context, root, ref string, o outputRefOpts) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	if o.attempts {
		return listOutputAttempts(ctx, m, ref, o.out)
	}
	if o.identity {
		return showOutputIdentity(ctx, m, ref, o.out)
	}
	if o.publish {
		return publishOutputRef(ctx, m, ref)
	}
	if o.open {
		return openOutputRef(ctx, m, ref, o)
	}
	// Remote-aware: a ref unknown locally may have been published from CI or a
	// teammate's machine, so the print path consults the remote bundle namespace
	// before reporting it missing.
	if o.out.Format != outputText {
		rec, rerr := m.OutputRecordByRef(ctx, ref)
		if rerr != nil {
			return reportRefLookupError(ctx, m, ref, rerr)
		}
		if o.out.Format == outputJSONL {
			// A slice, or writeJSONL would stream one of the record's list fields.
			return emitFormatted(o.out, []types.StoredOutput{rec})
		}
		return emitRecord(o.out, rec, []string{rec.Ref})
	}
	data, desc, err := m.OutputByRefRemote(ctx, ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	// On stderr, after the bytes: stdout stays pipe-clean, and a reader who has just
	// looked at what a run PRODUCED is one step from wanting to run it. Only worth
	// saying when the descriptor records a target to reproduce.
	if desc.Target != "" {
		interactive.Hint(ctx, fmt.Sprintf("reproduce this invocation here with `%s`", hint.X.With(ref)))
	}
	_, err = os.Stdout.Write(data) // default: verbatim bytes, pipe-clean
	if err == nil && desc.Failed && desc.ErrMsg != "" {
		// A failure's reason often lives only in the descriptor (a drift check prints
		// nothing), and only json showed it. It follows the bytes as a notice on stderr,
		// neutralized as an imported record's is, so stdout stays exactly what the run wrote.
		msg, _ := cache.NeutralizeTerminal([]byte(strings.TrimRight(desc.ErrMsg, "\n")), false)
		slog.ErrorContext(ctx, string(msg), attr.Notice(""), attr.Component("magus query output"))
		noticeFailedNeed(ctx, desc)
	}
	return err
}

func publishOutputRef(ctx context.Context, m *magus.Magus, ref string) error {
	published, perr := m.PublishOutput(ctx, ref)
	if perr != nil {
		if errors.Is(perr, fs.ErrNotExist) {
			// The lookup path's hint suggests --publish, which just failed.
			msg := fmt.Sprintf("no stored output for ref %q to publish; it may have aged out of the cache, or the ref is mistyped", ref)
			slog.ErrorContext(ctx, "", attr.Notice("magus query output"), attr.Error(types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg)))
			return exit.Reported(exit.Usage)
		}
		return fmt.Errorf("magus query output: publish %s: %w", ref, perr)
	}
	fmt.Printf("published %s to the remote cache\n", published)
	fmt.Printf("a teammate with the same trust set can now run: %s\n", hint.QueryOutput.With(published))
	return nil
}

// openOutputRef hands the viewer the ref's display events, which
// it renders from structure as a magus.viewer.v1alpha1 Journal.
func openOutputRef(ctx context.Context, m *magus.Magus, ref string, o outputRefOpts) error {
	data, desc, err := m.OutputByRef(ctx, ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	events := console.StitchDisplayEvents(data, cache.OutputDescriptor{
		Ref: desc.Ref, Project: desc.Project, Target: desc.Target, Inv: desc.Inv,
		Failed: desc.Failed, ErrMsg: desc.ErrMsg, TimestampMs: desc.TimestampMs, DurationMs: desc.DurationMs,
	})
	var inv magus.Invocation
	if desc.Inv != "" {
		// Best-effort lineage: omitted if the run log aged out.
		inv, _ = m.InvocationByID(desc.Inv) // a run log that aged out leaves the lineage empty
	}
	// The run's per-class key digests let the viewer compare keys across
	// machines. Best-effort: a run predating key-input persistence opens
	// without them.
	var keyDigests string
	if lines, lerr := m.OutputKeyInputs(ctx, ref); lerr == nil {
		pairs := make([]console.KeyClassDigest, 0, len(cache.ClassDigests(lines)))
		for _, c := range cache.ClassDigests(lines) {
			pairs = append(pairs, console.KeyClassDigest{Class: c.Class, Digest: c.Digest})
		}
		keyDigests = console.KeyDigestsParam(pairs)
	}
	return openOutputInViewer(ctx, desc, events, inv, keyDigests, o)
}

// noticeFailedNeed names the ctx.needs member a composed target failed on and the command
// that reads its output, which holds the findings the parent's own output and error only
// restate. It says nothing for a descriptor that records no member.
func noticeFailedNeed(ctx context.Context, d magus.OutputDescriptor) {
	if d.FailedNeedRef == "" {
		return
	}
	slog.InfoContext(ctx, fmt.Sprintf("the failing step is %s, a ctx.needs member; its output is under its own ref:", d.FailedNeedTarget),
		attr.Notice(""), attr.Component("magus query output"), attr.Next(hint.QueryOutput.With(d.FailedNeedRef)))
}

// trailPayloadRecord is the -o json/yaml projection of a stored payload: no run stands
// behind it, so the ref and the bytes are the whole record.
type trailPayloadRecord struct {
	Ref    string `json:"ref"`
	Output string `json:"output"`
}

// queryTrailPayload prints a payload the activity trail stored under a non-out prefix: a
// guard verdict (grd), an MCP request or response (mcp). The ref names its own store, so a
// miss here is final and the run-output store is not consulted.
func queryTrailPayload(ctx context.Context, root, ref string, out output.Options) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	data, err := m.PayloadByRef(ctx, ref)
	if errors.Is(err, fs.ErrNotExist) {
		msg := err.Error() + "; the activity trail may have rotated it out, or the ref is mistyped"
		slog.ErrorContext(ctx, "", attr.Notice("magus query output"), attr.Error(types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg)))
		return exit.Reported(exit.Usage)
	}
	if err != nil {
		return fmt.Errorf("magus query output: read %s: %w", ref, err)
	}
	if out.Format != outputText {
		return emitRecord(out, trailPayloadRecord{Ref: ref, Output: string(data)}, []string{ref})
	}
	// A verdict is stored without a trailing newline, so it would run into the prompt.
	if !bytes.HasSuffix(data, []byte("\n")) {
		data = append(data, '\n')
	}
	_, err = os.Stdout.Write(data)
	return err
}

// listOutputAttempts renders the keep-last-K executions behind one portable ref, newest
// first. Every attempt of a step shares the step's ref; the attempt id is the
// execution-unique handle, and passing a full attempt id to `magus query output`
// retrieves that exact execution's bytes.
func listOutputAttempts(ctx context.Context, m *magus.Magus, ref string, out output.Options) error {
	list, err := m.OutputAttempts(ctx, ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	if out.Format != outputText {
		names := make([]string, len(list))
		for i, d := range list {
			names[i] = d.Attempt
		}
		return emitRecord(out, list, names)
	}
	// Head the listing with the STEP's identity: the portable ref when a v2
	// descriptor carries the key, else the ref as the user typed it. list[0].Ref
	// would name one arbitrary execution for a pre-portable directory.
	stepRef := ref
	for _, d := range list {
		if d.Key != "" {
			stepRef = d.Ref
			break
		}
	}
	fmt.Printf("attempts for %s (newest first):\n", stepRef)
	for _, d := range list {
		status := "pass"
		if d.Failed {
			status = "fail"
		}
		when := time.UnixMilli(d.TimestampMs).Format("2006-01-02 15:04:05")
		dur := (time.Duration(d.DurationMs) * time.Millisecond).Round(time.Millisecond)
		fmt.Printf("  %s  %s  %8s  %s  %s\n", d.Attempt, status, dur, when, d.Inv)
	}
	// The bare ref already answers with the newest attempt; the hint earns its keep
	// for reaching an OLDER one.
	if len(list) > 1 {
		fmt.Printf("\nRetrieve one attempt's exact output: %s\n", hint.QueryOutput.With(list[len(list)-1].Attempt))
	}
	return nil
}

// invocationRecord is the machine-readable projection of one run: its header plus the events
// an audit cares about. recorder.Emit redacted the events on the way in, and a
// secret event carries only the reference and the provider by construction.
type invocationRecord struct {
	magus.Invocation
	Secrets []magus.Event `json:"secrets,omitempty"`
	Events  []magus.Event `json:"events,omitempty"`
}

// queryInvocation reads one run's journal back. `--secrets` narrows it to the credential
// reads, which is the question the secrets page promises an answer to: "which credentials did
// this run reach for, and through which backend".
//
// This exists because the events were written and unreachable. The reference and provider were
// recorded on every read, docs/concepts/secrets.md offered that as the audit trail, and the id
// magus prints (`inv:`) resolved to nothing, so the trail was a claim rather than a record.
func queryInvocation(ctx context.Context, root, inv string, secretsOnly bool, out output.Options) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	header, events, err := m.InvocationEventsByID(inv)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Aged out is the ordinary case, not a typo, so say which it might be. The cap is
			// the server's RotateLogs job; a missing log is indistinguishable from a bad id.
			slog.ErrorContext(ctx, fmt.Sprintf("no run log for %q; it may have aged out of the cache, or the id is mistyped", inv), attr.Notice("magus query invocation"))
			return exit.Reported(exit.Usage)
		}
		return fmt.Errorf("magus query invocation: read run log for %s: %w", inv, err)
	}

	var secrets []magus.Event
	for _, e := range events {
		if e.Kind == string(journal.KindSecret) {
			secrets = append(secrets, e)
		}
	}

	switch out.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		rec := invocationRecord{Invocation: header, Secrets: secrets}
		if !secretsOnly {
			rec.Events = events
		}
		return emitFormatted(out, rec)
	case outputName:
		return emitNames(os.Stdout, []string{header.ID})
	case outputText:
	}

	printInvocationText(os.Stdout, header, secrets, events, secretsOnly)
	return nil
}

func printInvocationText(w io.Writer, header magus.Invocation, secrets, events []magus.Event, secretsOnly bool) {
	fmt.Fprintf(w, "inv:     %s\n", header.ID)
	if len(header.Command.Arguments) > 0 {
		fmt.Fprintf(w, "command: magus %s\n", strings.Join(header.Command.Arguments, " "))
	}
	if header.Command.Cwd != "" {
		fmt.Fprintf(w, "cwd:     %s\n", header.Command.Cwd)
	}
	if header.StartedMs > 0 {
		line := time.UnixMilli(header.StartedMs).Format("2006-01-02 15:04:05")
		if header.FinishedMs > header.StartedMs {
			line += fmt.Sprintf(" (%s)", (time.Duration(header.FinishedMs-header.StartedMs) * time.Millisecond).Round(time.Millisecond))
		}
		fmt.Fprintf(w, "started: %s\n", line)
	}
	if header.Status != "" {
		fmt.Fprintf(w, "status:  %s\n", header.Status)
	}
	if header.MagusVersion != "" {
		fmt.Fprintf(w, "magus:   %s\n", header.MagusVersion)
	}

	fmt.Fprintln(w)
	if len(secrets) == 0 {
		// Say it plainly rather than printing an empty heading. "No credential reads" is a
		// real audit answer, and the reader must be able to tell it apart from "not recorded".
		// Same noun as the populated branch below, and as the rest of the CLI: this
		// line used to say "no credential was resolved", which read as a different fact.
		fmt.Fprintln(w, "secrets: no credential reads during this run")
	} else {
		fmt.Fprintf(w, "secrets: %d credential read(s)\n", len(secrets))
		for _, e := range secrets {
			where := e.Project
			if e.Target != "" {
				where += " " + e.Target
			}
			fmt.Fprintf(w, "  %s  %-28s %s\n", time.UnixMilli(e.Timestamp).Format("15:04:05"), where, e.Text)
		}
	}
	if secretsOnly {
		return
	}

	fmt.Fprintln(w)
	fmt.Fprintf(w, "events:  %d\n", len(events))
	for _, e := range events {
		if e.Kind == string(journal.KindOutput) {
			continue // the captured bytes belong to `query output <ref>`, not here
		}
		detail := e.Text
		if e.Kind == string(journal.KindRun) {
			// A result is the line a reader acts on: which target, and the ref that reads
			// its output, so a failure never needs a second lookup to find its log.
			first, _, _ := strings.Cut(e.Text, "\n")
			detail = strings.Join(slices.DeleteFunc([]string{e.Status, e.Project, e.Target, e.Ref, first}, func(s string) bool { return s == "" }), " ")
		} else if e.Status != "" {
			detail = strings.TrimSpace(fmt.Sprintf("%s %s", e.Status, detail))
		}
		fmt.Fprintf(w, "  %s  %-9s %s\n", time.UnixMilli(e.Timestamp).Format("15:04:05"), e.Kind, detail)
	}
}

// showOutputIdentity renders a stored run's IDENTITY rather than its output: what ran,
// how it ended, which invocation produced it, and the digests of its cache key's
// component classes: the machine-comparable half of the works-on-my-machine story
// (two machines compare digests to learn WHICH class disagrees; `describe target
// --cache --against` then names the exact line).
func showOutputIdentity(ctx context.Context, m *magus.Magus, ref string, out output.Options) error {
	desc, err := m.OutputDescriptorByRef(ctx, ref)
	if err != nil {
		return reportRefLookupError(ctx, m, ref, err)
	}
	var digests []cache.ClassDigest
	lines, lerr := m.OutputKeyInputs(ctx, ref)
	switch {
	case lerr == nil:
		digests = cache.ClassDigests(lines)
	case !errors.Is(lerr, fs.ErrNotExist):
		// Absent lines are ordinary (a run predating persistence); anything else is a
		// real read failure and must not masquerade as one.
		return fmt.Errorf("magus query output: read stored key inputs for %s: %w", ref, lerr)
	}
	var inv *magus.Invocation
	if desc.Inv != "" {
		if got, ierr := m.InvocationByID(desc.Inv); ierr == nil {
			inv = &got
		}
	}
	switch out.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(out, cache.OutputIdentityRecord[magus.Invocation]{OutputDescriptor: desc, Invocation: inv, ClassDigests: digests})
	case outputName:
		return emitNames(os.Stdout, []string{desc.Ref})
	case outputText:
	}
	var invArgs []string
	if inv != nil {
		invArgs = inv.Command.Arguments
	}
	cache.PrintOutputIdentityText(os.Stdout, desc, invArgs, digests, func() string {
		_, cur, _ := m.CurrentRevision(ctx)
		return cur
	})
	return nil
}

// reportRefLookupError renders the standard output-ref resolution failures (ambiguous prefix,
// missing/aged-out, or an unexpected error) as a coded diagnostic + exit code. On the
// missing-ref path it also prints a best-effort suggestion inverting the ref back to the
// workspace target(s) that could have minted it; see printIdentifyRefSuggestion. m may be
// nil at call sites with no loaded Magus in scope; the suggestion is then skipped, not
// attempted against a nil receiver.
func reportRefLookupError(ctx context.Context, m *magus.Magus, ref string, err error) error {
	var amb *cache.AmbiguousRefError
	switch {
	case errors.As(err, &amb):
		slog.ErrorContext(ctx, "", attr.Notice("magus query"), attr.Error(types.DiagnosticErrorf(types.OutputRefAmbiguous, "%s", amb.Error())))
		return exit.Reported(exit.Usage)
	case errors.Is(err, fs.ErrNotExist):
		// Name the stores consulted when the lookup knows them: a foreign ref that was
		// never published reads exactly like a mistyped one otherwise. Render the message
		// ONCE: RefNotFoundError.Error() already includes "consulted: <stores>".
		msg := fmt.Sprintf("no stored output for ref %q. It may have aged out of the cache, or the ref is mistyped; re-run the target to regenerate it.", ref)
		var missing *cache.RefNotFoundError
		if errors.As(err, &missing) {
			msg = missing.Error()
		}
		slog.ErrorContext(ctx, "", attr.Notice("magus query"), attr.Error(types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg)))
		printIdentifyRefSuggestion(ctx, m, ref)
		return exit.Reported(exit.Usage)
	default:
		return fmt.Errorf("magus query: look up output ref %q: %w", ref, err)
	}
}

// printIdentifyRefSuggestion best-effort-inverts a missing ref back to the workspace
// target(s) that could have minted it (Magus.IdentifyRef) and prints the finding to
// stderr. A nil m, or IdentifyRef itself erroring (e.g. types.ErrNoCache on an Inspect
// workspace), both skip silently: a best-effort suggestion must never turn a lookup
// error into a different one.
func printIdentifyRefSuggestion(ctx context.Context, m *magus.Magus, ref string) {
	if m == nil {
		return
	}
	matches, err := m.IdentifyRef(ctx, ref)
	if err != nil {
		return
	}
	switch len(matches) {
	case 0:
		// Informative, not a failure to try: nothing here keys to the ref because the
		// run that printed it had different inputs, not because this lookup is broken.
		slog.InfoContext(ctx, fmt.Sprintf("No target in this workspace keys to that ref at the current tree, which means the run that printed it had different inputs (a different commit, uncommitted change, or environment).\nOnce you know which target it should be: %s", hint.DescribeTargets.With("<target>", "--cache", "--against", ref)), attr.Notice(""))
	case 1:
		slog.InfoContext(ctx, "Nothing has produced it here, but this workspace would print it for:\n  "+m.RefMatchCommand(matches[0]), attr.Notice(""))
	default:
		var cmds strings.Builder
		for _, mt := range matches {
			cmds.WriteString("\n  " + m.RefMatchCommand(mt))
		}
		slog.InfoContext(ctx, "Nothing has produced it here, but this workspace would print it for any of:"+cmds.String(), attr.Notice(""))
	}
	// Reachable from every branch, not just the zero-match one: even a matched target
	// may be nondeterministic or expensive enough that the exact bytes from whoever
	// already has them beat a local re-run.
	slog.InfoContext(ctx, "If someone else has it, they can share it with: "+hint.QueryOutput.With(ref, "--publish"), attr.Notice(""))
}

// openOutputInViewer builds the viewer URL and opens a browser; --print emits the
// URL instead. It warns when the link nears browser URL-length limits.
func openOutputInViewer(ctx context.Context, desc magus.OutputDescriptor, events []journal.Record, inv magus.Invocation, keyDigests string, o outputRefOpts) error {
	if err := hostedPageAllowed(ctx, "log viewer", "magus query output --open", o.viewerBase, o.printURL); err != nil {
		return err
	}
	rawInv := journal.Invocation{
		ID:      inv.ID,
		Command: journal.Command{Arguments: inv.Command.Arguments, Cwd: inv.Command.Cwd, Trigger: journal.Trigger(inv.Command.Trigger)},
		Status:  journal.Status(inv.Status),
		Version: inv.MagusVersion,
	}
	if inv.StartedMs != 0 {
		rawInv.Started = time.UnixMilli(inv.StartedMs)
	}
	if inv.FinishedMs != 0 {
		rawInv.Finished = time.UnixMilli(inv.FinishedMs)
	}
	openURL, err := console.LogViewerURL(o.viewerBase, desc.Ref, events, rawInv, keyDigests)
	if err != nil {
		return err
	}
	if len(openURL) > interactive.FragmentWarnBytes {
		slog.WarnContext(ctx, fmt.Sprintf("this link is %d KB, near or past what Safari and older\n"+
			"Firefox accept in a URL (Chrome is fine). If the page does not load, pipe it instead:\n"+
			"  magus query output %s | less. Continuing.", len(openURL)/1024, desc.Ref), attr.Notice("magus query"))
	}
	if o.printURL {
		fmt.Println(openURL)
		return nil
	}
	slog.InfoContext(ctx, fmt.Sprintf("opening the log viewer for %s; the output rides in the link fragment and never leaves your machine.", desc.Ref), attr.Notice(""))
	if err := interactive.OpenBrowser(openURL); err != nil {
		slog.ErrorContext(ctx, "could not open a browser", attr.Notice("magus query"), attr.Error(err),
			attr.Why("Re-run with --print to get the URL."))
		return exit.Reported(exit.Failure)
	}
	return nil
}

// printSessionContact appends what the loaded agent sessions did to the file a node
// describes, or prints nothing when none of them reached it.
//
// WHY EXPLAIN CARRIES THIS. explain's job is one node's provenance, and it already prints
// the git half: vcs_commits, vcs_last_author, vcs_last_modified. Printing that a file was
// last changed by a person three weeks ago while staying silent about the four agent
// sessions that wrote it yesterday is not a neutral omission, because a reader draws a
// conclusion from the silence, and the conclusion is wrong. The two halves answer one
// question and belong in one place.
//
// It reads the session store rather than the @session overlay on purpose. That overlay is
// a lazy shard keyed to the symbol layer, so reaching it for a single node would load
// every code symbol in the workspace to learn about one file; the store is where the
// overlay's own input comes from, and a per-path read of it is cheap.
func printSessionContact(w io.Writer, root string, node types.KnowledgeNode) {
	if contact := sessionContact(root, node); contact != nil {
		sessions.PrintContact(w, *contact)
	}
}

// sessionContact is what the loaded sessions did to the file a node describes, or nil.
func sessionContact(root string, node types.KnowledgeNode) *sessions.PathContact {
	return sessions.ContactForNode(node, func() (string, error) { return sessions.Dir(resolveRootOrEmpty(root)) })
}
