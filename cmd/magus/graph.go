package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/workspacegraph"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/maintenance"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/oci"
	"github.com/egladman/magus/libs/secrets"
	"github.com/egladman/magus/libs/termkit"
	"github.com/egladman/magus/libs/xdg"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// `magus graph` is the graph meta-home: the workspace's graphs as objects.
// query/explain/path READ the knowledge graph (daily retrieval verbs); graph
// owns the graph ITSELF: emit the project dependency DAG (deps), export the
// merged knowledge graph for external tools (export), and report its shape
// (stats). One home instead of commands scattered across describe and insight.

var graphSubs = []string{"build", "push", "pull", "deps", "export", "stats", "diff"}

func graphCommand(ctx context.Context, root string, args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		graphUsage()
		return flag.ErrHelp
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "build":
		return graphBuild(ctx, root, rest)
	case "push":
		return graphPush(ctx, root, rest)
	case "pull":
		return graphPull(ctx, root, rest)
	case "deps":
		return graphDeps(ctx, root, rest)
	case "export":
		return graphExport(ctx, root, rest)
	case "stats":
		return graphStats(ctx, root, rest)
	case "diff":
		return graphDiff(ctx, root, rest)
	case "verify":
		// compat(until: no installed skill or published doc still names `graph verify`;
		// observe by grepping a fresh `magus agent install` tree and the released docs
		// for the string): every checkout whose skills predate v39 still tells an agent
		// to run this, and "unknown subcommand" routes them nowhere.
		//
		// Absent from graphSubs on purpose: suggesting it would teach the name back.
		return fmt.Errorf("magus graph verify has moved: run `magus doctor` and read its `agent skills` check, which grades the same installs and names the command that reinstalls them")
	default:
		slog.ErrorContext(ctx, fmt.Sprintf("unknown subcommand %q", sub), attr.Notice("magus graph"))
		if sug := hint.Nearest(sub, graphSubs); sug != "" {
			interactive.Hint(ctx, fmt.Sprintf("did you mean %q?", sug))
		}
		graphErrorUsage()
		return exit.Reported(exit.Usage)
	}
}

func graphErrorUsage() {
	fmt.Fprintln(os.Stderr, "")
	graphUsage()
}

func graphUsage() {
	fmt.Fprintln(os.Stderr, "Usage: magus graph <subcommand> [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The workspace's graphs as objects: emit, export, and measure them.")
	fmt.Fprintln(os.Stderr, "(query/explain/path read the knowledge graph; graph is the graph itself.)")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Subcommands:")
	fmt.Fprintln(os.Stderr, "  build    rebuild the knowledge graph now, reindexing code symbols (runs each project's scip op)")
	fmt.Fprintln(os.Stderr, "  push     publish the graph to a container registry as an OCI artifact")
	fmt.Fprintln(os.Stderr, "  pull     fetch a published graph; a public one needs no credentials")
	fmt.Fprintln(os.Stderr, "  deps     project dependency DAG (-o text|json|yaml|dot|tree)")
	fmt.Fprintln(os.Stderr, "  export   merged knowledge graph (-o json|graphml; --select for a dot neighborhood)")
	fmt.Fprintln(os.Stderr, "  stats    knowledge-graph shape: god nodes, orphans, doc coverage (--kind to scope)")
	fmt.Fprintln(os.Stderr, "  diff     nodes/edges added/removed/changed vs a baseline export or --rev; PR blast-radius")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "See also: magus query/explain/path (read the graph).")
}

// graphBuild is the explicit "build the graph now" subcommand: it reindexes code symbols
// (runs each symbol-capable project's scip op to refresh its cached SCIP index) and
// then forces a full knowledge-graph rebuild, re-ingesting those indexes. It exists
// because building is otherwise implicit (cache-first, on read), which leaves no obvious
// way to say "refresh everything now", especially the symbol indexes, which the server
// otherwise keeps fresh in the background. A missing indexer is reported with an install
// hint but does not fail the build; the domain graph rebuilds regardless. A reindex refused
// because an outer run holds the project's lock (MGS3007), or because the machine's build
// budget is full (MGS3009), fails the build once the rest is done.
func graphBuild(ctx context.Context, root string, args []string) (err error) {
	var bf *flaggen.GraphBuildFlags
	_, err = commandParse("graph build", args, func(fs *flag.FlagSet) {
		bf = flaggen.BindGraphBuild(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph build [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Rebuild the knowledge graph now. By default it first reindexes code symbols")
			fmt.Fprintln(os.Stderr, "by running each symbol-capable project's `scip` op, then runs each adapter")
			fmt.Fprintln(os.Stderr, "declared in knowledge.sessions to fold this machine's agent transcripts into")
			fmt.Fprintln(os.Stderr, "the @session overlay, then rebuilds and re-ingests. The server does this")
			fmt.Fprintln(os.Stderr, "automatically in the background; this is the manual trigger (after a branch")
			fmt.Fprintln(os.Stderr, "switch, or when the server is not running).")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	release, waitedOn, err := acquireGraphBuild(ctx, root)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	if waitedOn != nil {
		if current, err := loadBuiltGraph(ctx, root, *waitedOn); current || err != nil {
			return err
		}
	}

	var refused error
	var reindexed int
	if bf.Symbols {
		m, err := loadMagus(ctx, root)
		if err != nil {
			return err
		}
		defer func() { _ = m.Close() }()
		reindexed, refused = maintenance.ReindexSymbols(ctx, m.ReindexSymbols)
	}

	if bf.Sessions {
		ingestSessions(ctx, root)
	}

	// Claimed only now: a step behind its own process's claim may never seat.
	seated, err := admitGraphBuild(ctx, processBrokerClient(), root)
	if err != nil {
		return err
	}
	defer seated()

	g, err := loadKnowledgeGraph(ctx, root, true /* refresh */, false, false)
	if err != nil {
		return err
	}
	out := g.Output()
	slog.InfoContext(ctx, fmt.Sprintf("knowledge graph rebuilt: %d nodes, %d edges", out.NodeCount, out.EdgeCount), attr.Notice(""))
	scriptsErr := errors.Join(writeGuardIndexes(ctx, root), refused)
	omitted := knowledge.ShardErrors(config.CacheDir(root, globalCfg))
	scriptsErr = errors.Join(scriptsErr, knowledge.GraphBuildShardFailure(ctx, omitted))
	if opts.Format != outputText {
		rec := graphBuildOutput{Nodes: out.NodeCount, Edges: out.EdgeCount, Reindexed: reindexed, Refused: refused != nil, OmittedShards: omitted}
		if err := emitRecord(opts, rec, []string{root}); err != nil {
			scriptsErr = errors.Join(scriptsErr, err)
		}
	}
	return scriptsErr
}

type graphBuildOutput struct {
	Nodes         int      `json:"nodes"`
	Edges         int      `json:"edges"`
	Reindexed     int      `json:"reindexed"`
	Refused       bool     `json:"refused"`
	OmittedShards []string `json:"omitted_shards,omitempty"`
}

// skipIndex says what was not written and why, and answers nil: an index a hook reads is
// never a reason to fail the build.
func skipIndex(ctx context.Context, what string, err error) error {
	interactive.Hint(ctx, fmt.Sprintf("%s not written: %v", what, err))
	return nil
}

// writeGuardIndexes writes the indexes a guard hook reads, and returns only the scripts
// index's error. A guard index not written is a hint: the guard's graph-backed rules then
// stay silent, which is the state they were in before the build.
func writeGuardIndexes(ctx context.Context, root string) error {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return skipIndex(ctx, "guard indexes", err)
	}
	m, ok := ws.(*magus.Magus)
	if !ok {
		return nil
	}
	if err := m.WriteGuardIndex(ctx); err != nil {
		interactive.Hint(ctx, "guard index not written: "+err.Error())
	}
	if err := m.WriteScriptsIndex(ctx); err != nil {
		if errors.Is(err, hint.ErrScriptDeclaration) {
			return fmt.Errorf("magus graph build: %w", err)
		}
		interactive.Hint(ctx, "scripts index not written: "+err.Error())
	}
	return nil
}

// acquireGraphBuild takes the machine's build lock, then the store's, waiting
// on each and saying so. waitedOn is the build whose work this one can load.
func acquireGraphBuild(ctx context.Context, root string) (release func() error, waitedOn *maintenance.GraphBuildHolder, err error) {
	wsRoot := resolveRootOrEmpty(root)
	if wsRoot == "" {
		return nil, nil, errors.New("magus graph build: no workspace root here, so no knowledge store to build")
	}
	dir, err := knowledgeStoreDir(wsRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("magus graph build: %w", err)
	}
	notices := noticeLines{ctx: ctx, level: slog.LevelInfo, label: "magus graph build", prefix: "magus graph build: "}
	return maintenance.AcquireGraphBuilds(ctx, wsRoot, dir, notices)
}

// admitGraphBuild claims the machine budget for assembling the graph, under
// the workspace's broker policy.
func admitGraphBuild(ctx context.Context, admit cache.MachineAdmitter, root string) (func(), error) {
	return admitUnderBrokerPolicy(ctx, admit, root, "graph-build", maintenance.GraphBuildMemoryMB)
}

// noticeLines logs each line written to it as a notice. It stands in for os.Stderr where a
// helper takes a writer but is also handed stdout or a buffer, so the helper keeps its
// signature. A prefix names the label to move out of the text.
type noticeLines struct {
	ctx    context.Context //nolint:containedctx // class b: io.Writer has no ctx parameter; no optimization: comment or benchmark in its history (3269ac7f2)
	level  slog.Level
	label  string
	prefix string
}

func (w noticeLines) Write(p []byte) (int, error) {
	for line := range strings.Lines(string(p)) {
		msg := strings.TrimSuffix(line, "\n")
		if w.prefix != "" {
			msg = strings.TrimPrefix(msg, w.prefix)
		}
		slog.Log(w.ctx, w.level, msg, attr.Notice(w.label))
	}
	return len(p), nil
}

// loadBuiltGraph reports whether the build this one waited on left the graph current,
// and if so loads it cache-first instead of repeating its indexing, the expensive half. A
// project it left unindexed or behind, or a holder that died mid-build, returns false so
// the caller builds.
func loadBuiltGraph(ctx context.Context, root string, by maintenance.GraphBuildHolder) (bool, error) {
	left := staleIndexProjects(ctx, root)
	var gaps []types.KnowledgeSymbolGap
	probed := false
	if ws, err := inspectWorkspace(ctx, root); err == nil {
		gaps, probed = workspacegraph.SymbolGaps(ctx, ws, ws.Root(), globalCfg, slog.Default())
	}
	if len(gaps) > 0 {
		left = append(left, types.DescribeGaps(gaps))
	}
	if !probed {
		left = append(left, "the symbol indexes (unreadable)")
	}
	if len(left) > 0 {
		slog.InfoContext(ctx, fmt.Sprintf("%s finished and left these without a current index: %s; building here",
			by, strings.Join(left, ", ")), attr.Notice("magus graph build"))
		return false, nil
	}
	g, err := loadKnowledgeGraph(ctx, root, false, false, false)
	if err != nil {
		return true, err
	}
	out := g.Output()
	slog.InfoContext(ctx, fmt.Sprintf("knowledge graph current, built by %s: %d nodes, %d edges", by, out.NodeCount, out.EdgeCount), attr.Notice(""))
	return true, nil
}

// ingestSessions runs the declared transcript adapters, reporting each one's own summary
// line, before the graph that reads what they loaded is assembled.
//
// Never fatal, for the same reason reindexing is not: an adapter that fails leaves the
// previous ingest in place, so the graph is missing its newest sessions rather than
// wrong, and failing the rebuild over it would take the other nine tenths of the graph
// down with it. A workspace that declares no adapter says nothing at all, since silence
// is the correct report for a feature nobody opted into.
func ingestSessions(ctx context.Context, root string) {
	cfg := globalCfg.Knowledge.Sessions
	if cfg.Disabled {
		return
	}
	// The config-to-store mapping lives HERE, in the composition root, so neither package
	// names the other's vocabulary: config owns the workspace schema, internal/sessions
	// owns the store and takes two strings per adapter.
	adapters := make([]sessions.Adapter, 0, len(cfg.Adapters))
	for _, a := range cfg.Adapters {
		adapters = append(adapters, sessions.Adapter{Host: a.Host, Argv: a.Command})
	}
	// Resolved here rather than taken as given: root is empty on every invocation that did
	// not pass --root, and an adapter is a command with a working directory, so it has to
	// be a real path before one can run.
	results := sessions.RunAdapters(ctx, resolveRootOrEmpty(root), adapters)
	for _, r := range results {
		if r.Err != nil {
			interactive.Hint(ctx, fmt.Sprintf("session adapter %s did not finish", r.Host), attr.Error(r.Err))
		}
		if r.Output != "" {
			slog.InfoContext(ctx, r.Output, attr.Notice(""))
		}
	}
}

// graphDeps emits the project dependency DAG, the standalone home of the view
// `magus run <target> --graph` and `magus affected <target> --graph` scope to a
// run (those flags remain as scoped passthroughs).
func graphDeps(ctx context.Context, root string, args []string) error {
	var gf *flaggen.GraphDepsFlags
	pos, err := commandParse("graph deps", args, func(fs *flag.FlagSet) {
		gf = flaggen.BindGraphDeps(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph deps [flags] [project...]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Emit the project dependency DAG. A trailing list of project paths roots")
			fmt.Fprintln(os.Stderr, "the graph at those projects; default is the whole workspace. The same")
			fmt.Fprintf(os.Stderr, "view scoped to a run is available as `%s` and\n", hint.Run.With("<target>", "--graph"))
			fmt.Fprintf(os.Stderr, "`%s`.\n", hint.Affected.With("<target>", "--graph"))
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	return renderWorkspaceGraph(ctx, ws, graphRenderOptions{
		Upstream: gf.Upstream,
		Depth:    gf.Depth,
		Spell:    gf.Spell,
		Roots:    pos,
		Target:   gf.Target,
	})
}

// graphExport emits the merged knowledge graph: it assembles the deterministic
// graph (projects, targets, spells, ops, charms, modules, methods, diagnostics,
// docs, buzz sources), persists it as fingerprinted shards under
// <cache>/knowledge, and writes the node-link export. The cache-first loader
// makes building implicit: there is no separate build subcommand.
func graphExport(ctx context.Context, root string, args []string) error {
	var ef *flaggen.GraphExportFlags
	pos, err := commandParse("graph export", args, func(fs *flag.FlagSet) {
		ef = flaggen.BindGraphExport(fs, flaggen.GraphExportDefaults{URL: console.HostedExplorerURL, Budget: knowledge.DefaultBudget})
		fs.Usage = func() { graphExportUsage(fs) }
	})
	if err != nil {
		return err
	}

	// --open is a different DESTINATION, not a different format, so it short-circuits
	// before any -o resolution: the explorer takes the graph in the shape the explorer
	// wants, and -o json/graphml stay the paths that hand it to another tool.
	if ef.Open {
		return openExplorer(ctx, root, explorerOptions{
			refresh:     ef.Refresh,
			globalScope: ef.Global,
			base:        ef.URL,
			printOnly:   ef.Print,
			serve:       ef.Serve,
			useTargets:  ef.Targets,
			follow:      ef.Follow,
		}, pos)
	}

	opts, err := output.Parse(global.output, outputGraphML, outputDot, outputSCIP)
	if err != nil {
		return err
	}
	if opts.Format == outputSCIP {
		return graphExportSCIP(ctx, root, os.Stdout, termkit.SystemProbe, ef.Select != "")
	}
	// dot is a graph-layout format; on the whole graph (1000s of nodes) it is
	// unreadable, so it requires a --select neighborhood to scope down.
	if opts.Format == outputDot && ef.Select == "" {
		return fmt.Errorf("-o %s requires --select \"<terms>\" to scope the export, the full graph is too large to lay out (use -o json or -o graphml for the whole graph)", opts.Format)
	}

	// The whole-graph export stays domain-only unless --symbols asks; a --select neighborhood
	// pulls in the symbol shards only when the selection actually targets symbols.
	g, err := loadKnowledgeGraph(ctx, root, ef.Refresh, ef.Global, ef.Symbols || (ef.Select != "" && knowledge.SeedsLazyLayer(ef.Select)))
	if err != nil {
		return err
	}
	out := g.Output()
	if ef.Select != "" {
		out = g.Select(ef.Select, ef.Budget)
		if out.NodeCount == 0 {
			slog.WarnContext(ctx, fmt.Sprintf("no nodes matched --select %q", ef.Select), attr.Notice("magus graph export"))
		}
	}
	if ef.Reproducible {
		knowledge.StripUnreproducible(&out)
	}
	// Omitted under --reproducible for the same reason as everything else it drops: the
	// fingerprint identifies the BINARY, not the graph, so two builds of one source rewrote
	// this field and failed the drift gate with the fingerprint as the entire diff.
	//
	// Every other export still carries it, which is where it answers its question:
	// when two graphs disagree, which build produced each.
	if !ef.Reproducible {
		out.CatalogFingerprint = magus.CatalogFingerprint()
	}
	// The blob base lets a viewer link a node's relative `source` to the right repo.
	// A --global union spans many repos, so a single base would be wrong: leave it off.
	if !ef.Global {
		out.SourceBaseURL = deriveSourceBase(ctx, root)
	}

	return writeGraphExport(opts, out)
}

func writeGraphExport(opts output.Options, out types.KnowledgeGraphOutput) error {
	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputGraphML:
		return render.WriteKnowledgeGraphML(os.Stdout, out)
	case outputDot:
		return render.WriteKnowledgeDOT(os.Stdout, out)
	case outputName:
		names := make([]string, 0, len(out.Nodes))
		for _, n := range out.Nodes {
			names = append(names, n.ID)
		}
		return emitNames(os.Stdout, names)
	}
	knowledge.WriteSummary(os.Stdout, out)
	return nil
}

func graphExportUsage(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stderr, "Usage: magus graph export [flags]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, types.KnowledgeGraphDefinition)
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Emits the merged graph: -o json for the node-link form, -o graphml for")
	fmt.Fprintln(os.Stderr, "GraphML (Gephi, yEd, and other graph viewers read both directly). The")
	fmt.Fprintln(os.Stderr, "graph is cache-backed under <cache>/knowledge; only shards whose sources")
	fmt.Fprintln(os.Stderr, "changed are rebuilt. -o scip writes the symbol index instead, as one binary")
	fmt.Fprintln(os.Stderr, "SCIP index for code-intelligence tools: redirect it to a file.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "--open sends the same graph to the interactive Graph Explorer instead of")
	fmt.Fprintln(os.Stderr, "stdout. It never leaves your machine: by default it rides in the link's URL")
	fmt.Fprintln(os.Stderr, "fragment (#data=...), which browsers never transmit; --serve hands it over an")
	fmt.Fprintln(os.Stderr, "ephemeral 127.0.0.1 loopback server instead (no size limit). --targets opens")
	fmt.Fprintln(os.Stderr, "the target dependency graph, and takes an optional project path to scope it.")
	fmt.Fprintf(os.Stderr, "--follow keeps the view updating from the running server (%s);\n", hint.ServerStart)
	fmt.Fprintln(os.Stderr, "with no mode flag and a reachable server it is chosen automatically.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "--select \"<terms>\" narrows the export to a query's neighborhood, sharing")
	fmt.Fprintf(os.Stderr, "the engine behind `%s`. -o dot renders only with\n", hint.Query.String())
	fmt.Fprintln(os.Stderr, "--select: the full graph has too many nodes for that layout to be legible.")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
	printOwnDefaults(fs)
}

// outputSCIP is graph export's SCIP protobuf index, for code-intelligence tools.
const outputSCIP output.Format = "scip"

// graphExportSCIP writes the workspace's symbol indexes to out as one SCIP index, judging
// out a terminal by probe. --select narrows the knowledge graph, which this export does
// not carry, so it is refused rather than ignored.
func graphExportSCIP(ctx context.Context, root string, out io.Writer, probe termkit.Probe, selected bool) error {
	if selected {
		return fmt.Errorf("-o %s exports the whole symbol index, and --select narrows the knowledge graph, which it does not carry: drop --select", outputSCIP)
	}
	if termkit.IsTerminalWriter(out, probe) {
		return fmt.Errorf("-o %[1]s writes a binary SCIP index: redirect it to a file, as in magus graph export -o %[1]s > index.scip", outputSCIP)
	}
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	return m.ExportSCIP(ctx, out)
}

// graphStats reports the knowledge graph's shape: god nodes, orphans, and doc
// coverage. It reads the graph cache-first rather than git history, the
// structural companion to insight's history lenses (insight report embeds it).
func graphStats(ctx context.Context, root string, args []string) error {
	var sf *flaggen.GraphStatsFlags
	_, err := commandParse("graph stats", args, func(fs *flag.FlagSet) {
		sf = flaggen.BindGraphStats(fs)
		fs.Usage = func() {
			fmt.Fprintf(os.Stderr, "Usage: magus graph stats [flags]\n\n%s\n\nFlags (global flags also accepted, see `magus -h`):\n", types.KnowledgeStatsDefinition)
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	outOpts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	// Stats stay domain-only unless --symbols (or a --kind symbol scope) opts in.
	g, err := loadKnowledgeGraph(ctx, root, sf.Refresh, sf.Global, sf.Symbols || sf.Kind == types.KindSymbol)
	if err != nil {
		return err
	}
	out := g.Stats(sf.Kind)

	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, out)
	case outputName:
		names := make([]string, 0, len(out.Gods))
		for _, god := range out.Gods {
			names = append(names, god.ID)
		}
		return emitNames(os.Stdout, names)
	}
	return knowledge.StatsText(os.Stdout, out)
}

// loadKnowledgeGraph gathers the workspace inputs and runs the cache-first build,
// returning the merged in-memory graph. Shared by the graph subcommands and the
// query/explain/path verbs so they all sit on one substrate. When global is set,
// it unions the workspaces registered in config (knowledge.workspaces) with the
// current one, namespacing node IDs by workspace. When includeSymbols is set (a
// symbol-seeded query), the lazily-loaded @symbols shards are merged in on top of
// the default domain graph.
func loadKnowledgeGraph(ctx context.Context, root string, refresh, global, includeSymbols bool) (*knowledge.Graph, error) {
	ws, err := inspectForRead(ctx, root)
	if err != nil {
		return nil, err
	}
	return graphReads.LoadGraph(ctx, ws, globalCfg, refresh, global, includeSymbols)
}

// inspectForRead is inspectWorkspace for a graph read: the handle startup already opened
// when there is one, else a lazy workspace that inspects on the first call needing the
// model, so a read the stored graph answers never evaluates a magusfile (see openForRead).
func inspectForRead(_ context.Context, root string) (workspacegraph.ReadWorkspace, error) { //nolint:ireturn // the workspace backing is chosen at runtime
	if m, ok := loadedMagus(); ok {
		return m, nil
	}
	wsRoot, err := workspace.FindRoot(root)
	if err != nil {
		return nil, err
	}
	return magus.NewLazyWorkspace(wsRoot, func(ctx context.Context) (*magus.Magus, error) {
		ws, err := inspectWorkspace(ctx, root)
		if err != nil {
			return nil, err
		}
		return asMagus(ws)
	}), nil
}

// loadKnowledgeGraphForRefs builds the domain graph and merges the symbol shards a
// `magus refs` lookup needs: targeted to ref's shards via the xref routing index when
// ref is an exact symbol ID, else all symbol shards. Symbols are per-workspace, so
// refs does not take --global.
func loadKnowledgeGraphForRefs(ctx context.Context, root string, refresh bool, ref string) (*knowledge.Graph, error) {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return nil, err
	}
	return loadRefsGraph(ctx, ws, globalCfg, refresh, ref)
}

func loadRefsGraph(ctx context.Context, ws types.WorkspaceRepository, cfg config.Config, refresh bool, ref string) (*knowledge.Graph, error) {
	g, err := workspacegraph.Build(ctx, ws, ws.Root(), cfg, refresh, slog.Default())
	if err != nil {
		return nil, err
	}
	if err := workspacegraph.MergeSymbolsForRef(ctx, ws, ws.Root(), cfg, g, ref, slog.Default()); err != nil {
		return nil, err
	}
	return g, nil
}

// ---- project-DAG rendering shared by graph deps, run --graph, and affected --graph ----

type graphRenderOptions struct {
	Upstream bool
	Depth    int
	Spell    string
	Roots    []string
	// Target is the target whose duration history to show (e.g. "build").
	// Falls back to "build" when empty.
	Target string
}

// renderWorkspaceGraph emits the project dependency graph; respects -o (text|json|yaml|dot|tree).
func renderWorkspaceGraph(ctx context.Context, ws types.WorkspaceRepository, opts graphRenderOptions) error {
	outOpts, err := output.Parse(global.output, outputDot, outputTree)
	if err != nil {
		return err
	}

	g, err := ws.Graph()
	if err != nil {
		return err
	}

	target := opts.Target
	if target == "" {
		target = "build"
	}

	// Load timing history best-effort; silently skip when unavailable.
	composeOpts := []magus.ComposeOption{magus.WithGraphInput(g)}
	if opts.Upstream {
		composeOpts = append(composeOpts, magus.WithUpstream())
	}
	if opts.Spell != "" {
		composeOpts = append(composeOpts, magus.WithComposeSpell(opts.Spell))
	}
	if len(opts.Roots) > 0 {
		composeOpts = append(composeOpts, magus.WithComposeRoots(opts.Roots...))
	}
	if path := globalCfg.HistoryPath; path != "" {
		var hist forecast.History
		if err := hist.Load(ctx, path); err == nil {
			composeOpts = append(composeOpts, magus.WithGraphHistory(&hist, target))
		}
	}

	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, magus.ComposeGraph(ws, composeOpts...))
	case outputName:
		out := magus.ComposeGraph(ws, composeOpts...)
		names := make([]string, 0, len(out.Nodes))
		for _, n := range out.Nodes {
			names = append(names, n.Path)
		}
		return emitNames(os.Stdout, names)
	case outputDot:
		return render.WriteGraphDOT(os.Stdout, magus.ComposeGraph(ws, composeOpts...))
	}

	// text and tree formats both render the ASCII dependency tree.
	var rOpts []render.RenderOption
	if opts.Upstream {
		rOpts = append(rOpts, render.WithDirection(types.Upstream))
	}
	if opts.Spell != "" {
		rOpts = append(rOpts, render.WithSpell(opts.Spell))
	}
	if opts.Depth != 0 {
		rOpts = append(rOpts, render.WithMaxDepth(opts.Depth))
	}
	if len(opts.Roots) > 0 {
		rOpts = append(rOpts, render.WithRoots(opts.Roots...))
	}
	return render.WriteTree(os.Stdout, g, rOpts...)
}

// ---- graph export --open: delivering the graph to the Graph Explorer ----

// `open` points a browser at console.HostedExplorerURL with the workspace's graph
// delivered PRIVATELY: either in a URL fragment (default) or fetched from an ephemeral
// loopback server (--serve). Either way the graph stays on the machine: the site only
// serves static assets.

// Two privacy-first delivery modes:
//   - default: gzip+base64url the graph into a `#data=` URL fragment. A fragment
//     is never sent in an HTTP request, so the graph never leaves the machine.
//     Simple and serverless, but bounded by browser URL limits.
//   - --serve: run an ephemeral loopback HTTP server (127.0.0.1) that serves the
//     graph to the page via `#src=`. No size limit; the data stays on the local
//     network (loopback), never reaching the hosted site. CORS is locked to the
//     site origin so no other page can read it.
//
// explorerOptions is what `magus graph export --open` passes down: the delivery mode
// and scope already parsed, so this file no longer owns a flag set of its own.
type explorerOptions struct {
	refresh     bool
	globalScope bool
	base        string
	printOnly   bool
	serve       bool
	useTargets  bool
	follow      bool
}

// openExplorer delivers the graph to the Graph Explorer.
//
// It used to be `magus graph export --open`, its own subcommand with its own flags. Opening a
// viewer was spelled two ways across the CLI (a subcommand here, a --open FLAG on
// `magus query output`) for one act. It is a flag everywhere now, and export is its
// host because export is already the verb that emits the graph: -o json hands it to
// another tool, --open hands it to the viewer.
func openExplorer(ctx context.Context, root string, o explorerOptions, pos []string) error {
	refresh, globalScope, base := o.refresh, o.globalScope, o.base
	printOnly, serve, useTargets, follow := o.printOnly, o.serve, o.useTargets, o.follow

	if useTargets {
		if serve {
			slog.ErrorContext(ctx, "--targets and --serve cannot be used together.", attr.Notice("magus graph export --open"),
				attr.Why("Target graphs are small; they always use the URL fragment."))
			return exit.Reported(exit.Usage)
		}
		if globalScope {
			slog.ErrorContext(ctx, "--targets and --global cannot be used together.", attr.Notice("magus graph export --open"),
				attr.Why("--targets scopes to this workspace's target graph; use a positional argument to scope to one project."))
			return exit.Reported(exit.Usage)
		}
		if refresh {
			slog.ErrorContext(ctx, "--targets and --refresh cannot be used together.", attr.Notice("magus graph export --open"),
				attr.Why("--targets reads the target graph directly from the magusfile; there is no knowledge store to refresh."))
			return exit.Reported(exit.Usage)
		}
		if err := hostedPageAllowed(ctx, "graph explorer", "magus graph export --open", base, printOnly); err != nil {
			return err
		}
		return graphOpenTargets(ctx, root, base, printOnly, pos)
	}

	// Zero-arg default for the interactive open: when no explicit delivery mode is
	// chosen and no --targets, probe the ACTUAL console first (not just the proc
	// socket: a proc server can be up with no bridge running). If it is reachable,
	// use --follow for an always-fresh view; otherwise fall through to fragment mode.
	// Skip the auto-probe under --print: that flag exists for scriptable, copyable
	// output, so its URL must be deterministic (the static data fragment) rather than
	// flipping to a live+token URL whenever a server happens to be listening. Explicit
	// --follow --print still prints the follow URL.
	if !follow && !serve && !printOnly {
		if httpx.LiveBridgeReachable(ctx, mcpAddrString()) {
			follow = true
		}
	}
	if follow {
		return graphOpenFollow(ctx, root, printOnly, useTargets)
	}
	if err := hostedPageAllowed(ctx, "graph explorer", "magus graph export --open", base, printOnly); err != nil {
		return err
	}

	// The explorer shows the domain graph; symbol shards would bloat it, so exclude them.
	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope, false)
	if err != nil {
		return err
	}
	out := g.Output()
	if !globalScope {
		out.SourceBaseURL = deriveSourceBase(ctx, root) // link node sources to the right repo
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode graph: %w", err)
	}

	if serve {
		return graphOpenServe(ctx, base, raw, out.NodeCount, out.EdgeCount)
	}

	encoded, err := render.EncodeFragmentRaw(raw)
	if err != nil {
		return err
	}
	return interactive.OpenGraphFragment(ctx, base, encoded, out.NodeCount, out.EdgeCount, printOnly)
}

// hostedPageAllowed refuses to open a page mcp.allowed_origins does not name.
// The page receives workspace data, so opening it is a trust decision.
// --print opens nothing, so it passes.
func hostedPageAllowed(ctx context.Context, page, notice, base string, printOnly bool) error {
	if printOnly {
		return nil
	}
	err := httpx.CheckHostedPageOrigin(base, globalCfg.MCP.AllowedOrigins)
	if err == nil {
		return nil
	}
	slog.ErrorContext(ctx, "refused to open the "+page, attr.Notice(notice), attr.Error(err),
		attr.Why("Add the origin to mcp.allowed_origins in magus.yaml, pass --url a loopback mirror, or use --print for the link alone."))
	return exit.Reported(exit.Failure)
}

// graphOpenTargets opens the workspace's target dependency graph in the hosted
// Graph Explorer using the #data= fragment path. Target graphs are always
// delivered via the fragment (they are small, so --serve is never needed).
// If args contains a project path, only that project's targets are included.
func graphOpenTargets(ctx context.Context, root, base string, printOnly bool, args []string) error {
	ws, err := inspectWorkspace(ctx, root)
	if err != nil {
		return err
	}
	out, err := ws.TargetGraph(ctx)
	if err != nil {
		return err
	}

	if len(args) > 0 {
		scope := args[0]
		var filtered []types.TargetGraphProject
		for _, p := range out.Projects {
			if p.Path == scope {
				filtered = append(filtered, p)
				break
			}
		}
		if len(filtered) == 0 {
			paths := make([]string, 0, len(out.Projects))
			for _, p := range out.Projects {
				paths = append(paths, p.Path)
			}
			slices.Sort(paths)
			slog.ErrorContext(ctx, fmt.Sprintf("unknown project %q", scope), attr.Notice("magus graph export --open --targets"),
				attr.Why("valid projects:\n  "+strings.Join(paths, "\n  ")))
			return exit.Reported(exit.Usage)
		}
		out.Projects = filtered
	}

	raw, err := json.Marshal(out)
	if err != nil {
		return fmt.Errorf("encode target graph: %w", err)
	}
	encoded, err := render.EncodeFragmentRaw(raw)
	if err != nil {
		return err
	}
	openURL := fmt.Sprintf("%s/#data=%s", strings.TrimRight(base, "/"), encoded)

	if printOnly {
		fmt.Println(openURL)
		return nil
	}

	slog.InfoContext(ctx, "opening the graph explorer for this workspace's target graph.\n"+
		"your graph rides in the link fragment and is never uploaded - it does not leave your machine.", attr.Notice(""))
	if err := interactive.OpenBrowser(openURL); err != nil {
		slog.ErrorContext(ctx, "could not open a browser", attr.Notice("magus graph export --open"), attr.Error(err),
			attr.Why("Re-run with --print to get the URL, or open it yourself."))
		return exit.Reported(exit.Failure)
	}
	return nil
}

// graphOpenServe hands the graph to the hosted page over an ephemeral 127.0.0.1 server,
// then STOPS: a one-shot handoff, not a standing service. The loopback bind, CORS lock,
// serve-once, and grace-then-shutdown all live in internal/httpx (shared with the live log
// stream); this wraps them with the graph-specific URL (#src=) and the user-facing
// messages. The graph is delivered browser <-> loopback and never leaves the machine.
func graphOpenServe(ctx context.Context, base string, raw []byte, nodes, edges int) error {
	origin, err := httpx.ParseOrigin(base)
	if err != nil {
		return err
	}
	bs, err := httpx.StartBlob(origin, "/graph.json", "application/json", raw)
	if err != nil {
		return err
	}
	// SourceURL carries the per-run bearer token in a `?token=` query param; tucking the
	// whole URL into the `#src=` fragment keeps the token out of any HTTP request the browser
	// makes to the hosted page, and the explorer replays it when it fetches the blob.
	openURL := fmt.Sprintf("%s/#src=%s", strings.TrimRight(base, "/"), url.QueryEscape(bs.SourceURL()))

	slog.InfoContext(ctx, fmt.Sprintf("handing this workspace's graph (%d nodes, %d edges) to your browser over loopback (%s).\n"+
		"it is served once, CORS-locked to %s, and never leaves your machine; the server stops as soon as the page has it.",
		nodes, edges, bs.Addr(), origin), attr.Notice(""))
	if err := interactive.OpenBrowser(openURL); err != nil {
		slog.WarnContext(ctx, "could not open a browser", attr.Notice("magus graph export --open"), attr.Error(err),
			attr.Why("Open this yourself (the server is waiting):\n  "+openURL))
	}

	switch outcome := bs.WaitServed(ctx); outcome {
	case httpx.ServeCompleted:
		slog.InfoContext(ctx, "graph loaded; loopback server stopped.", attr.Notice(""))
	case httpx.ServeTimedOut:
		slog.InfoContext(ctx, "the page never requested the graph; loopback server stopped. Re-run if your browser did not open.", attr.Notice(""))
	case httpx.ServeCanceled:
		slog.InfoContext(ctx, "\ncanceled; loopback server stopped.", attr.Notice(""))
	default:
		// A new ServeOutcome added upstream must not be swallowed as success: name it.
		return fmt.Errorf("graph export --open --serve: unexpected serve outcome %v", outcome)
	}
	return nil
}

// graphOpenLive opens the Graph Explorer served BY the running server from its own
// loopback origin (http://<host>/console/graph/). Under the server-origin grammar the origin
// names which server; the page loads both itself and its graph data from that one loopback
// origin, so the graph never leaves the machine. The clean /console/graph/ path is canonical:
// the server serves the shell for it and the console's boot router opens the graph app.
// There is no #live= host directive and no hosted explorer base: the --url flag governs only
// the static (--data/--targets/--serve) modes, not --follow.
//
// The link's fragment carries a one-time code, never a token. The page trades
// it within seconds, so the opener's argv holds nothing worth stealing.
func graphOpenFollow(ctx context.Context, root string, printOnly, useTargets bool) error {
	hostPort := mcpAddrString()

	// The ACTUAL console is probed (not just the proc socket) so we never emit a URL and
	// token for a transport nothing is listening on. When nothing serves it, the server is
	// started rather than refused: --follow is a request for the console, and the console
	// is the server's own front end.
	//
	// --print is exempt. It is the scriptable "just give me the URL" form, and a command
	// that only prints must not leave a background process behind as a side effect.
	if printOnly {
		pctx, cancel := context.WithTimeout(ctx, httpx.ProbeLiveBridgeTimeout)
		defer cancel()
		if err := httpx.ProbeLiveBridge(pctx, hostPort); err != nil {
			slog.ErrorContext(ctx, "the console is not reachable.", attr.Notice("magus graph export --open --follow --print"),
				attr.Why("start it: "+hint.ServerStart.String()))
			return exit.Reported(exit.Failure)
		}
	} else if err := ensureConsoleServer(ctx, hostPort, root); err != nil {
		slog.ErrorContext(ctx, "", attr.Notice("magus graph export --open --follow"), attr.Error(err),
			attr.Why("start it yourself to see the server's own output: "+hint.ServerStart.String()))
		return exit.Reported(exit.Failure)
	}

	code, err := mintConsoleLinkCode()
	if err != nil {
		slog.ErrorContext(ctx, "could not mint a sign-in code for the link", attr.Notice("magus graph export --open --follow"), attr.Error(err))
		return exit.Reported(exit.Failure)
	}

	linkOpts := console.LinkOpts{Host: hostPort, App: "graph", Code: code}
	if useTargets {
		linkOpts.Fragment = append(linkOpts.Fragment, console.FragmentParam{Key: "flavor", Value: "targets"})
	}
	openURL := console.Link(linkOpts)

	if printOnly {
		fmt.Println(openURL)
		return nil
	}

	slog.InfoContext(ctx, fmt.Sprintf("opening the graph explorer in live mode (server at %s).\n"+
		"the explorer connects directly to your local server; your graph never leaves your machine.", hostPort), attr.Notice(""))
	if err := interactive.OpenBrowser(openURL); err != nil {
		slog.ErrorContext(ctx, "could not open a browser", attr.Notice("magus graph export --open"), attr.Error(err),
			attr.Why("Re-run with --print to get the URL, or open it yourself."))
		return exit.Reported(exit.Failure)
	}
	return nil
}

// graphDiff reports how the knowledge graph changed relative to a baseline: the nodes
// and edges added, removed, or changed. It is the PR-review blast-radius artifact: emit
// it as json or markdown for a CI comment. The baseline is either an export file
// (`magus graph export -o json`, e.g. from the base branch) or, with --rev, a git
// revision whose tracked files are built into a base graph on the fly.
func graphDiff(ctx context.Context, root string, args []string) error {
	var df *flaggen.GraphDiffFlags
	pos, err := commandParse("graph diff", args, func(fs *flag.FlagSet) {
		df = flaggen.BindGraphDiff(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph diff <baseline.json> [flags]")
			fmt.Fprintln(os.Stderr, "       magus graph diff --rev <revision> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeGraphDiffDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The positional argument is a whole-graph export produced earlier with")
			fmt.Fprintf(os.Stderr, "`%s`; the current working-tree graph is diffed against it.\n", hint.GraphExport.With("-o", "json"))
			fmt.Fprintln(os.Stderr, "Symbol shards in the baseline are matched automatically; pass --global if it was global.")
			fmt.Fprintln(os.Stderr, "With --rev, the base graph is built from that revision's tracked files (domain-only,")
			fmt.Fprintln(os.Stderr, "using the current config); no export file is needed. --rev and the positional are exclusive.")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	gotRev, gotFile := df.Rev != "", len(pos) > 0
	if gotRev == gotFile { // neither or both
		slog.ErrorContext(ctx, "give exactly one of a baseline export file or --rev <revision>", attr.Notice("magus graph diff"))
		return exit.Reported(exit.Usage)
	}
	if gotFile && len(pos) > 1 {
		slog.ErrorContext(ctx, "takes a single baseline export file", attr.Notice("magus graph diff"))
		return exit.Reported(exit.Usage)
	}
	if gotRev && df.Global {
		// A --rev base is always a single-workspace domain-only build, while --global
		// qualifies the current side's node IDs by workspace; diffing the two would
		// report every node added/removed. Reject rather than emit a garbage diff.
		slog.ErrorContext(ctx, "--rev cannot be combined with --global (the base is built single-workspace)", attr.Notice("magus graph diff"))
		return exit.Reported(exit.Usage)
	}

	outOpts, err := output.Parse(global.output, outputMarkdown)
	if err != nil {
		return err
	}

	baseline, baseLabel, err := diffBaseline(ctx, root, df.Rev, pos)
	if err != nil {
		return err
	}

	// Build the current graph to match the baseline's shape: if the baseline carries
	// symbol nodes (a symbol-seeded export), include symbols here too, else every
	// baseline symbol would surface as a spurious removal. A --rev baseline is always
	// domain-only (an archive has no built .scip indexes), so the current side is too.
	g, err := loadKnowledgeGraph(ctx, root, df.Refresh, df.Global, knowledge.BaselineHasSymbols(baseline))
	if err != nil {
		return err
	}
	current := g.Output()
	// @vcs enrichment (opt-in) stamps commit-varying attrs on file nodes. They are not
	// domain shape, and the base side never has them (a --rev export tree has no history,
	// and an exported baseline predates the current commit), so keeping them would report
	// nearly every file node as changed. Strip them from both sides for a structural diff.
	knowledge.StripVCSAttrs(&baseline)
	knowledge.StripVCSAttrs(&current)
	diff := knowledge.DiffGraphs(baseLabel, baseline, current)

	switch outOpts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(outOpts, diff)
	case outputMarkdown:
		_, err := os.Stdout.Write(knowledge.RenderDiffMarkdown(diff))
		return err
	case outputName:
		return emitNames(os.Stdout, knowledge.DiffNames(diff))
	}
	return knowledge.DiffText(os.Stdout, diff)
}

// diffBaseline resolves the base graph to diff against: either a git revision (--rev,
// built from that revision's tracked files) or a positional export file. It returns the
// base graph and a human label for it (the revision or the file path).
func diffBaseline(ctx context.Context, root, rev string, pos []string) (types.KnowledgeGraphOutput, string, error) {
	if rev != "" {
		g, err := baseGraphFromRev(ctx, root, rev)
		if err != nil {
			return types.KnowledgeGraphOutput{}, "", err
		}
		return g, rev, nil
	}
	baselinePath := pos[0]
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		return types.KnowledgeGraphOutput{}, "", fmt.Errorf("graph diff: read baseline %q: %w", baselinePath, err)
	}
	var baseline types.KnowledgeGraphOutput
	if err := json.Unmarshal(raw, &baseline); err != nil {
		return types.KnowledgeGraphOutput{}, "", fmt.Errorf("graph diff: decode baseline %q (expected `%s` output): %w", baselinePath, hint.GraphExport.With("-o", "json"), err)
	}
	return baseline, baselinePath, nil
}

// baseGraphFromRev builds a base knowledge graph from a revision's tracked files. It
// materializes the revision into a throwaway temp tree via the VCS abstraction (any
// backend implementing RevisionExporter; git does, and one that does not gives a clear
// error), then runs the ordinary extraction pipeline there via a direct Inspect (NOT the
// memoized inspectWorkspace, which panics on a second root). The result is domain-only
// and reflects the CURRENT config applied to the revision's files; a historical-config
// diff would need the rev's own config threaded through, deliberately out of scope here.
//
// The base build is pinned to an isolated, immutable cache under the temp tree: without
// this, an absolute cache.dir / MAGUS_CACHE_DIR would make resolveCacheDir ignore the
// temp root, and the base build would read the CURRENT workspace's shards as its starting
// point (corrupting the base) and prune live shards on write. Isolated + immutable means
// it assembles in memory and touches nothing outside the temp tree.
func baseGraphFromRev(ctx context.Context, root, rev string) (types.KnowledgeGraphOutput, error) {
	res, err := vcs.Resolve(ctx, root, "", types.VCSOptions{})
	if err != nil || res.Source == types.VCSSourceDisabled || res.VCS == nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: --rev needs version control, but none resolved for this workspace")
	}
	tmp, err := xdg.ScratchCheckoutDir("graph-diff")
	if err != nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: create temp tree: %w", err)
	}
	defer os.RemoveAll(tmp)

	if err := res.VCS.ExportRevision(ctx, root, rev, tmp); err != nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: export revision %q: %w", rev, err)
	}

	cfg := globalCfg
	cfg.Cache.Dir = filepath.Join(tmp, ".magus-base-cache") // absolute -> wins over any env cache dir
	writeOff := false
	cfg.Cache.Write.Enabled = &writeOff // a throwaway base tree must publish nothing
	// The exported tree has no VCS metadata, so history enrichment can't run against it;
	// disable it so the base is not asymmetrically missing @vcs attrs the current side has.
	cfg.Knowledge.VCS.Enabled = false

	// No workspace providers on the base side: the export is a bare tree (no
	// node_modules, no toolchain, no VCS metadata), so a provider shelling out to nx
	// or gradle there would fail and take the whole diff with it. A project only a
	// provider knows about therefore reads as added rather than unchanged, which is
	// the honest answer for a base magus cannot resolve.
	ws, err := magus.Inspect(ctx, tmp, append(cliOptions(), workspace.WithLoadedConfig(cfg), magus.WithoutWorkspaceProviders())...)
	if err != nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: inspect revision %q tree: %w", rev, err)
	}
	g, err := workspacegraph.Build(ctx, ws, ws.Root(), cfg, false, slog.Default())
	if err != nil {
		return types.KnowledgeGraphOutput{}, fmt.Errorf("graph diff: build graph for revision %q: %w", rev, err)
	}
	return g.Output(), nil
}

// graph push and graph pull below are flags and wiring; the registry artifact, its
// media types and its transport live in internal/graph/knowledge (publish.go).

// graphRegistryTimeout bounds the whole exchange for every registry verb in this binary.
const graphRegistryTimeout = knowledge.RegistryTimeout

// readToken takes the registry token from STDIN; spell.go's verbs read theirs the same way.
func readToken(ctx context.Context) (secrets.Secret, error) { return knowledge.ReadToken(ctx) }

func graphPush(ctx context.Context, root string, args []string) error {
	var pf *flaggen.GraphPushFlags
	_, err := commandParse("graph push", args, func(fs *flag.FlagSet) {
		pf = flaggen.BindGraphPush(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph push [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Publish this workspace's knowledge graph to a container registry as an OCI")
			fmt.Fprintln(os.Stderr, "artifact, so collaborators can `magus graph pull` it instead of rebuilding.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The destination is never derived: --ref names it, tag included, and the")
			fmt.Fprintln(os.Stderr, "magusfile is where a workspace declares one, beside the registries its")
			fmt.Fprintln(os.Stderr, "images use. One invocation writes one tag; a caller that wants several")
			fmt.Fprintln(os.Stderr, "calls this once per tag, where its own naming vocabulary lives.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The token is read from STDIN, the way `docker login --password-stdin` takes")
			fmt.Fprintln(os.Stderr, "one, so it never lands in a process listing or a run log. Whatever resolves")
			fmt.Fprintln(os.Stderr, "it (magus\\secret.read, a CI secret, a password manager) stays the caller's")
			fmt.Fprintln(os.Stderr, "choice; this never learns which.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A newly created package is PRIVATE until someone makes it public; until then")
			fmt.Fprintln(os.Stderr, "`magus graph pull` cannot read it anonymously.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	var rec knowledge.RegistryOutput
	if pf.Table {
		if pf.Refresh {
			return errors.New("graph push: -refresh rebuilds the graph, and -table publishes no graph")
		}
		rec, err = knowledge.TablePush(ctx, pf.Ref, pf.Username, registryHTTP())
	} else {
		rec, err = pushGraph(ctx, root, pf.Ref, pf.Username, pf.Refresh)
	}
	if err != nil || opts.Format == outputText {
		return err
	}
	return emitRecord(opts, rec, []string{rec.Ref})
}

// pushGraph publishes the knowledge graph to ref, rebuilding it first under refresh.
func pushGraph(ctx context.Context, root, ref, user string, refresh bool) (knowledge.RegistryOutput, error) {
	return knowledge.PushGraph(ctx, ref, user, func() ([]oci.Blob, error) {
		if _, err := loadKnowledgeGraph(ctx, root, refresh, false, true /* includeSymbols */); err != nil {
			return nil, err
		}
		// Off the workspace's own root, not the caller's --root argument, which may be empty
		// or name a directory inside the workspace; the store the build just wrote is the one
		// that root resolves to.
		ws, err := inspectWorkspace(ctx, root)
		if err != nil {
			return nil, err
		}
		cacheDir, err := magus.ResolveCacheDir(ws.Root(), workspace.WithLoadedConfig(globalCfg))
		if err != nil {
			return nil, fmt.Errorf("graph push: %w", err)
		}
		return knowledge.StoreLayers(cacheDir)
	})
}

func graphPull(ctx context.Context, root string, args []string) error {
	var pf *flaggen.GraphPullFlags
	_, err := commandParse("graph pull", args, func(fs *flag.FlagSet) {
		pf = flaggen.BindGraphPull(fs)
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus graph pull [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Fetch a knowledge graph someone published with `magus graph push`.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A PUBLIC artifact needs no credentials at all. The graph is written to")
			fmt.Fprintln(os.Stderr, "stdout unless --out names a file, so it composes with anything that reads")
			fmt.Fprintln(os.Stderr, "the node-link JSON `magus graph export -o json` emits.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			printOwnDefaults(fs)
		}
	})
	if err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}
	ref, out := pf.Ref, pf.Out

	if pf.Table {
		if out != "" {
			return errors.New("graph pull: -table writes the table to the cache, so --out names nothing")
		}
		rec, err := knowledge.TablePull(ctx, ref, registryHTTP())
		if err != nil || opts.Format == outputText {
			return err
		}
		return emitRecord(opts, rec, []string{rec.Ref})
	}
	if ref == "" {
		return knowledge.NoGraphDestinationError("pull")
	}
	if out == "" && opts.Format != outputText {
		return exit.Usagef("magus graph pull: -o needs --out, since stdout is the graph")
	}
	src, err := knowledge.GraphReference(ref, knowledge.GraphFloatingTag)
	if err != nil {
		return err
	}
	// No credentials on purpose: a public artifact is the case this exists for, and a
	// client that quietly used an ambient token would work for whoever built it and fail
	// for everyone they handed it to.
	client := &oci.Client{HTTP: &http.Client{Timeout: graphRegistryTimeout}}
	ctx, cancel := context.WithTimeout(ctx, graphRegistryTimeout)
	defer cancel()

	raw, err := knowledge.PullMergedGraph(ctx, client, src, deriveSourceBase(ctx, root))
	if err != nil {
		return fmt.Errorf("graph pull: %w", err)
	}
	if out == "" {
		_, err := os.Stdout.Write(raw)
		return err
	}
	if err := os.WriteFile(out, raw, 0o644); err != nil {
		return fmt.Errorf("graph pull: %w", err)
	}
	slog.InfoContext(ctx, fmt.Sprintf("pulled %s into %s (%d bytes)", src, out, len(raw)), attr.Notice(""))
	if opts.Format != outputText {
		return emitRecord(opts, knowledge.RegistryOutput{Ref: src.String(), Bytes: len(raw), Path: out}, []string{src.String()})
	}
	return nil
}
