package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/graph/url"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/render"
	"github.com/egladman/magus/internal/service/console"
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

func queryCmd(ctx context.Context, root string, args []string) error {
	var (
		budget      int
		kinds       string
		refresh     bool
		globalScope bool
		open        bool
		printURL    bool
		viewerBase  string
		attempts    bool
		meta        bool
		publish     bool
	)
	pos, err := cmdParse("query", args, func(fs *flag.FlagSet) {
		fs.IntVar(&budget, "budget", 0, "max nodes in the returned neighborhood (default 50)")
		fs.StringVar(&kinds, "kind", "", "restrict matches to these node kinds (comma-separated)")
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before querying")
		fs.BoolVar(&globalScope, "global", false, "query across the workspaces registered in config (knowledge.workspaces); IDs are namespaced by workspace")
		fs.BoolVar(&open, "open", false, "with `output <ref>`, open the captured output in the browser log viewer (delivered privately; never uploaded)")
		fs.BoolVar(&printURL, "print", false, "with --open, print the viewer URL instead of launching a browser")
		fs.StringVar(&viewerBase, "url", defaultLogViewerURL, "with --open, base URL of the log viewer page (override for a self-hosted mirror)")
		fs.BoolVar(&attempts, "attempts", false, "with `output <ref>`, list the ref's stored executions (newest first) instead of printing output")
		fs.BoolVar(&meta, "meta", false, "with `output <ref>`, show the run's identity instead of its output: descriptor, lineage, cache key, component digests")
		fs.BoolVar(&publish, "publish", false, "with `output <ref>`, upload this run's output to the remote cache as a signed bundle so a teammate can resolve the same ref (failing runs are never shared automatically)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus query <terms> [flags]")
			fmt.Fprintln(os.Stderr, "       magus query output <ref> [-o json] [--open] [--attempts] [--meta]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeQueryDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Terms are free text plus field filters: kind:spell, project:pkg/foo,")
			fmt.Fprintln(os.Stderr, "relation:uses, id:build, and negation (-kind:op). Example:")
			fmt.Fprintln(os.Stderr, "  magus query kind:spell go")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintf(os.Stderr, "`%s <ref>` retrieves one target execution's captured output by its\n", clihint.QueryOutput.Leaf())
			fmt.Fprintln(os.Stderr, "reference id (out1a2b3c), shown when the target ran:")
			fmt.Fprintf(os.Stderr, "  %-38s print the exact bytes (pipe anywhere)\n", clihint.QueryOutput.With("out1a2b3c"))
			fmt.Fprintf(os.Stderr, "  %-38s the descriptor + output as a record\n", clihint.QueryOutput.With("out1a2b3c", "-o json"))
			fmt.Fprintf(os.Stderr, "  %-38s open it in the browser log viewer\n", clihint.QueryOutput.With("out1a2b3c", "--open"))
			fmt.Fprintf(os.Stderr, "  %-38s list the ref's stored executions\n", clihint.QueryOutput.With("out1a2b3c", "--attempts"))
			fmt.Fprintf(os.Stderr, "  %-38s the run's identity + cache-key digests\n", clihint.QueryOutput.With("out1a2b3c", "--meta"))
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "--open respects the BROWSER environment variable to pick the browser")
			fmt.Fprintln(os.Stderr, "(e.g. BROWSER=firefox); otherwise it uses your desktop's default handler.")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}

	// Output-reference retrieval is an EXPLICIT subcommand - `magus query output <ref>` - not a
	// shape-routed positional, so a search term can never collide with a ref id.
	if len(pos) >= 1 && pos[0] == clihint.QueryOutput.Leaf() {
		if len(pos) != 2 {
			fmt.Fprintf(os.Stderr, "%s: expected exactly one ref (e.g. %s)\n", clihint.QueryOutput, clihint.QueryOutput.With("out1a2b3c"))
			return errSilent{exitCode: 2}
		}
		ref := pos[1]
		if !cache.LooksLikeRef(ref) {
			msg := fmt.Sprintf("%q is not a target-output reference (expected out<hex>, e.g. out1a2b3c)", ref)
			fmt.Fprintf(os.Stderr, "magus query output: %s\n", types.DiagnosticErrorf(types.OutputRefMalformed, "%s", msg).Error())
			return errSilent{exitCode: 2}
		}
		outOpts, oerr := outputOptionsOrDefault()
		if oerr != nil {
			return oerr
		}
		exclusive := 0
		for _, set := range []bool{attempts, meta, publish, open || printURL} {
			if set {
				exclusive++
			}
		}
		if exclusive > 1 {
			fmt.Fprintf(os.Stderr, "magus query output: --attempts, --meta, --publish, and --open/--print are distinct actions; pick one\n")
			return errSilent{exitCode: 2}
		}
		return queryOutputRef(ctx, root, ref, outputRefOpts{open: open, printURL: printURL, viewerBase: viewerBase, attempts: attempts, meta: meta, publish: publish, out: outOpts})
	}
	if open || printURL || attempts || meta || publish {
		// --open/--print/--attempts/--meta only apply to `query output <ref>`. Set on a graph
		// search, they were a mistake; stop rather than silently ignore them.
		fmt.Fprintf(os.Stderr, "magus query: --open/--print/--attempts/--meta/--publish apply only to `%s <ref>`. To open the knowledge graph in a browser, use `%s`.\n", clihint.QueryOutput, clihint.GraphOpen)
		return errSilent{exitCode: 2}
	}
	if len(pos) == 0 && kinds == "" {
		fmt.Fprintln(os.Stderr, "magus query: requires search terms")
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	input := strings.Join(pos, " ")
	for _, k := range splitCSV(kinds) {
		input += " kind:" + k
	}

	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope, knowledge.SeedsSymbols(input))
	if err != nil {
		return err
	}
	out := g.Query(input, budget)

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		for _, m := range out.Matches {
			fmt.Println(m.ID)
		}
		return nil
	}

	fmt.Printf("query: %s\n", out.Query)
	fmt.Printf("matches: %d  (neighborhood budget %d)\n\n", out.MatchCount, out.Budget)
	shown := out.Matches
	if len(shown) > 20 {
		shown = shown[:20]
	}
	for _, m := range shown {
		fmt.Printf("  %-7d %s  [%s]\n", m.Score, m.ID, m.Kind)
	}
	if len(out.Matches) > len(shown) {
		fmt.Printf("  ... and %d more\n", len(out.Matches)-len(shown))
	}
	fmt.Printf("\nneighborhood: %d nodes, %d edges\n", len(out.Nodes), len(out.Links))
	fmt.Println("Run with -o json for the full subgraph.")
	return nil
}

// outputRefOpts carries the options for `magus query output <ref>`.
type outputRefOpts struct {
	open       bool          // open the browser log viewer instead of printing
	printURL   bool          // with open, print the URL instead of launching a browser
	viewerBase string        // log viewer base URL
	attempts   bool          // list the ref's stored executions instead of printing output
	meta       bool          // show the run's identity (descriptor, lineage, key digests)
	publish    bool          // upload this run's output to the remote as a signed bundle
	out        OutputOptions // -o: text prints raw bytes, json/yaml prints the descriptor record
}

// outputRefRecord is the -o json/yaml projection of a stored output: its descriptor plus the
// captured output as an opaque verbatim field. The descriptor DESCRIBES the run (project/target/
// status/timing); the output is the payload, never parsed - so structure lives in the record,
// not in an interpretation of the bytes.
type outputRefRecord struct {
	cache.OutputDescriptor
	Output string `json:"output"`
}

// queryOutputRef retrieves a target's captured output by reference id (or unique prefix). The
// default prints the exact bytes to stdout (pipe-friendly); -o json/yaml prints the descriptor
// record; --open hands it to the browser log viewer. The bytes never leave the machine: --open
// rides them in a URL fragment, exactly like `magus graph open`.
func queryOutputRef(ctx context.Context, root, ref string, o outputRefOpts) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	if o.attempts {
		return listOutputAttempts(m, ref, o.out)
	}
	if o.meta {
		return showOutputMeta(m, ref, o.out)
	}
	if o.publish {
		published, perr := m.PublishOutput(ctx, ref)
		if perr != nil {
			if errors.Is(perr, fs.ErrNotExist) {
				return reportRefLookupError(ref, perr)
			}
			return fmt.Errorf("magus query output: publish %s: %w", ref, perr)
		}
		fmt.Printf("published %s to the remote cache\n", published)
		fmt.Printf("a teammate with the same trust set can now run: %s\n", clihint.QueryOutput.With(published))
		return nil
	}
	if o.open {
		// The viewer ingests a magus.viewer.v1 Journal, so hand it the ref's display events -
		// the browser renders pretty from structure.
		data, desc, err := m.OutputByRef(ref)
		if err != nil {
			return reportRefLookupError(ref, err)
		}
		events := console.StitchDisplayEvents(data, desc)
		var inv journal.Invocation
		if desc.Inv != "" {
			inv, _ = m.InvocationByID(desc.Inv) // best-effort lineage; omitted if the run log aged out
		}
		// The run's per-class key digests ride along so the viewer can show a
		// machine-vs-machine key comparison. Best-effort: a run predating key-line
		// persistence just opens without them.
		var keyDigests string
		if lines, lerr := m.OutputKeyLines(ref); lerr == nil {
			d := cache.ClassDigests(lines)
			classes := make([]string, len(d))
			digests := make([]string, len(d))
			for i, c := range d {
				classes[i], digests[i] = c.Class, c.Digest
			}
			keyDigests = console.KeyDigestsParam(classes, digests)
		}
		return openOutputInViewer(desc, events, inv, keyDigests, o)
	}
	// Remote-aware: a ref unknown locally may have been published from CI or a
	// teammate's machine, so the print path consults the remote bundle namespace
	// before reporting it missing.
	data, desc, err := m.OutputByRefRemote(ctx, ref)
	if err != nil {
		return reportRefLookupError(ref, err)
	}
	if o.out.Format == FormatJSON || o.out.Format == FormatYAML {
		return emitFormatted(o.out, outputRefRecord{OutputDescriptor: desc, Output: string(data)})
	}
	_, err = os.Stdout.Write(data) // default: verbatim bytes, pipe-clean
	return err
}

// listOutputAttempts renders the keep-last-K executions behind one portable ref, newest
// first. Every attempt of a step shares the step's ref; the attempt id is the
// execution-unique handle, and passing a full attempt id to `magus query output`
// retrieves that exact execution's bytes.
func listOutputAttempts(m *magus.Magus, ref string, out OutputOptions) error {
	list, err := m.OutputAttempts(ref)
	if err != nil {
		return reportRefLookupError(ref, err)
	}
	if out.Format == FormatJSON || out.Format == FormatYAML {
		return emitFormatted(out, list)
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
		fmt.Printf("\nRetrieve one attempt's exact output: %s\n", clihint.QueryOutput.With(list[len(list)-1].Attempt))
	}
	return nil
}

// outputMetaRecord is the -o json/yaml projection of `query output <ref> --meta`: the
// stored descriptor, the producing invocation's lineage when its run log survives, and
// the cache key's component-class digests when the run persisted its key lines.
type outputMetaRecord struct {
	cache.OutputDescriptor
	Invocation   *journal.Invocation `json:"invocation,omitempty"`
	ClassDigests []cache.ClassDigest `json:"class_digests,omitempty"`
}

// showOutputMeta renders a stored run's IDENTITY rather than its output: what ran,
// how it ended, which invocation produced it, and the digests of its cache key's
// component classes - the machine-comparable half of the works-on-my-machine story
// (two machines compare digests to learn WHICH class disagrees; `describe target
// --cache --against` then names the exact line).
func showOutputMeta(m *magus.Magus, ref string, out OutputOptions) error {
	desc, err := m.OutputDescriptorByRef(ref)
	if err != nil {
		return reportRefLookupError(ref, err)
	}
	var digests []cache.ClassDigest
	lines, lerr := m.OutputKeyLines(ref)
	switch {
	case lerr == nil:
		digests = cache.ClassDigests(lines)
	case !errors.Is(lerr, fs.ErrNotExist):
		// Absent lines are ordinary (a run predating persistence); anything else is a
		// real read failure and must not masquerade as one.
		return fmt.Errorf("magus query output: read stored key lines for %s: %w", ref, lerr)
	}
	var inv *journal.Invocation
	if desc.Inv != "" {
		if got, ierr := m.InvocationByID(desc.Inv); ierr == nil {
			inv = &got
		}
	}
	switch out.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(out, outputMetaRecord{OutputDescriptor: desc, Invocation: inv, ClassDigests: digests})
	case outputName:
		fmt.Println(desc.Ref)
		return nil
	}
	fmt.Printf("ref:     %s\n", desc.Ref)
	fmt.Printf("project: %s\n", desc.Project)
	if desc.Target != "" {
		fmt.Printf("target:  %s\n", desc.Target)
	}
	status := "pass"
	if desc.Failed {
		status = "fail"
	}
	fmt.Printf("status:  %s (%s) at %s\n", status,
		(time.Duration(desc.DurationMs) * time.Millisecond).Round(time.Millisecond),
		time.UnixMilli(desc.TimestampMs).Format("2006-01-02 15:04:05"))
	if desc.ErrMsg != "" {
		fmt.Printf("error:   %s\n", desc.ErrMsg)
	}
	if desc.Attempt != "" {
		fmt.Printf("attempt: %s\n", desc.Attempt)
	}
	if desc.Inv != "" {
		fmt.Printf("inv:     %s", desc.Inv)
		if inv != nil && len(inv.Command.Arguments) > 0 {
			fmt.Printf("  (magus %s)", strings.Join(inv.Command.Arguments, " "))
		}
		fmt.Println()
	}
	if desc.Key != "" {
		fmt.Printf("key:     %s (keyVersion %d)\n", desc.Key, desc.KeyVersion)
	}
	if desc.MagusVersion != "" {
		fmt.Printf("magus:   %s\n", desc.MagusVersion)
	}
	if len(digests) == 0 {
		fmt.Println("\nkey components: unavailable (run predates key-line persistence; re-run the target to record them)")
		return nil
	}
	fmt.Println("\nkey components:")
	for _, d := range digests {
		noun := "lines"
		if d.Count == 1 {
			noun = "line"
		}
		fmt.Printf("  %-16s %s  %d %s\n", d.Class, d.Digest, d.Count, noun)
	}
	return nil
}

// reportRefLookupError renders the standard output-ref resolution failures (ambiguous prefix,
// missing/aged-out, or an unexpected error) as a coded diagnostic + exit code.
func reportRefLookupError(ref string, err error) error {
	var amb *cache.AmbiguousRefError
	switch {
	case errors.As(err, &amb):
		fmt.Fprintf(os.Stderr, "magus query: %s\n", types.DiagnosticErrorf(types.OutputRefAmbiguous, "%s", amb.Error()).Error())
		return errSilent{exitCode: 2}
	case errors.Is(err, fs.ErrNotExist):
		// Name the stores consulted when the lookup knows them: a foreign ref that was
		// never published reads exactly like a mistyped one otherwise.
		msg := fmt.Sprintf("no stored output for ref %q. It may have aged out of the cache, or the ref is mistyped; re-run the target to regenerate it.", ref)
		var missing *cache.RefNotFoundError
		if errors.As(err, &missing) {
			msg = fmt.Sprintf("%s (consulted: %s). If it came from CI or a teammate, they can share it with `%s`.",
				missing.Error(), strings.Join(missing.Stores, ", "), clihint.QueryOutput.With(ref, "--publish"))
		}
		fmt.Fprintf(os.Stderr, "magus query: %s\n", types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg).Error())
		return errSilent{exitCode: 2}
	default:
		return fmt.Errorf("magus query: look up output ref %q: %w", ref, err)
	}
}

// openOutputInViewer builds the viewer URL and opens a browser; --print emits the
// URL instead. It warns when the link nears browser URL-length limits.
func openOutputInViewer(desc cache.OutputDescriptor, events []journal.Event, inv journal.Invocation, keyDigests string, o outputRefOpts) error {
	openURL, err := console.LogViewerURLWithKey(o.viewerBase, desc.Ref, events, inv, keyDigests)
	if err != nil {
		return err
	}
	if len(openURL) > fragmentWarnBytes {
		fmt.Fprintf(os.Stderr, "magus query: this link is %d KB, near or past what Safari and older\n", len(openURL)/1024)
		fmt.Fprintf(os.Stderr, "Firefox accept in a URL (Chrome is fine). If the page does not load, pipe it instead:\n")
		fmt.Fprintf(os.Stderr, "  magus query output %s | less. Continuing.\n", desc.Ref)
	}
	if o.printURL {
		fmt.Println(openURL)
		return nil
	}
	fmt.Fprintf(os.Stderr, "opening the log viewer for %s; the output rides in the link fragment and never leaves your machine.\n", desc.Ref)
	if err := openBrowser(openURL); err != nil {
		fmt.Fprintf(os.Stderr, "magus query: could not open a browser (%v). Re-run with --print to get the URL.\n", err)
		return errSilent{exitCode: 1}
	}
	return nil
}

func explainCmd(ctx context.Context, root string, args []string) error {
	var (
		refresh     bool
		globalScope bool
	)
	pos, err := cmdParse("explain", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before explaining")
		fs.BoolVar(&globalScope, "global", false, "resolve across the workspaces registered in config (knowledge.workspaces)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus explain <node-id-or-name> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeExplainDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "The argument is a node ID (target:pkg/foo:build) or a name that resolves")
			fmt.Fprintln(os.Stderr, "to one (build). Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		fmt.Fprintln(os.Stderr, "magus explain: requires a node ID or name")
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope, knowledge.SeedsSymbols(pos[0]))
	if err != nil {
		return err
	}
	out, ok := g.Explain(pos[0])
	if !ok {
		fmt.Fprintf(os.Stderr, "magus explain: no node matches %q\n", pos[0])
		return errSilent{exitCode: 2}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	case outputName:
		fmt.Println(out.Node.ID)
		return nil
	}

	fmt.Print(render.ExplainText(out))

	// Complementary deep-link: focus this node in the live Graph Explorer with a
	// blast view (the console's own analogue of `magus explain`). Symbol nodes are
	// excluded from the live full graph the explorer loads, so a link to one would
	// open to an empty focus - omit it. The link is always printed for other kinds;
	// the daemon may not be up when the browser opens it, hence the hint.
	if out.Node.Kind != types.KindSymbol {
		link := liveExplorerLink(url.GraphLinkOpts{View: "blast", Node: out.Node.ID})
		fmt.Printf("\nView in Graph Explorer: %s\n", link)
		fmt.Printf("(start the magus daemon if the graph does not load)\n")
	}
	return nil
}

func pathCmd(ctx context.Context, root string, args []string) error {
	var (
		refresh     bool
		globalScope bool
	)
	pos, err := cmdParse("path", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before pathfinding")
		fs.BoolVar(&globalScope, "global", false, "resolve endpoints across the workspaces registered in config (knowledge.workspaces)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus path <a> <b> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgePathDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Each argument is a node ID or a name that resolves to one.")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
	}
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, "magus path: requires two node IDs or names")
		return errSilent{exitCode: 2}
	}

	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	g, err := loadKnowledgeGraph(ctx, root, refresh, globalScope, knowledge.SeedsSymbols(pos[0]) || knowledge.SeedsSymbols(pos[1]))
	if err != nil {
		return err
	}
	out, ok := g.Path(pos[0], pos[1])
	if !ok {
		fmt.Fprintf(os.Stderr, "magus path: could not resolve %q or %q to a node\n", pos[0], pos[1])
		return errSilent{exitCode: 2}
	}

	switch opts.Format {
	case outputJSON, outputYAML, outputJSONL, outputTemplate:
		return emitFormatted(opts, out)
	}

	fmt.Print(render.PathText(out))
	return nil
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
