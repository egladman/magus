package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/types"
)

// query/explain/path are the knowledge-graph retrieval verbs. They reuse
// prior-art vocabulary (graph tooling generally) and sit on the same cache-first
// substrate as `magus graph export`. query resolves terms to nodes and returns
// the neighborhood; explain shows one node's context; path connects two nodes.
//
// query also doubles as the retrieval verb for target-output reference ids: a
// positional shaped like a ref (strict ^ref[0-9a-f]+$, see cache.LooksLikeRef)
// prints that execution's captured output instead of searching the graph. Reusing
// query here - rather than a dedicated subcommand - keeps the CLI surface small.

// defaultLogViewerURL is the hosted, data-agnostic log viewer that `magus query
// ref... --open` points a browser at, with the captured output delivered PRIVATELY
// in a URL fragment (never uploaded). Override with --url for a self-hosted mirror.
const defaultLogViewerURL = "https://eli.gladman.cc/magus/logs/"

func queryCmd(ctx context.Context, root string, args []string) error {
	var (
		budget      int
		kinds       string
		refresh     bool
		globalScope bool
		meta        bool
		open        bool
		printURL    bool
		viewerURL   string
	)
	pos, err := cmdParse("query", args, func(fs *flag.FlagSet) {
		fs.IntVar(&budget, "budget", 0, "max nodes in the returned neighborhood (default 50)")
		fs.StringVar(&kinds, "kind", "", "restrict matches to these node kinds (comma-separated)")
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before querying")
		fs.BoolVar(&globalScope, "global", false, "query across the workspaces registered in config (knowledge.workspaces); IDs are namespaced by workspace")
		fs.BoolVar(&meta, "meta", false, "with a ref argument, prepend a header (ref, project, target, status) before the output")
		fs.BoolVar(&open, "open", false, "with a ref argument, open the captured output in the browser log viewer (delivered privately; never uploaded)")
		fs.BoolVar(&printURL, "print", false, "with --open, print the viewer URL instead of launching a browser")
		fs.StringVar(&viewerURL, "url", defaultLogViewerURL, "with --open, base URL of the log viewer page (override for a self-hosted mirror)")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus query <terms|ref> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeQueryDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Terms are free text plus field filters: kind:spell, project:pkg/foo,")
			fmt.Fprintln(os.Stderr, "relation:uses, id:build, and negation (-kind:op). Example:")
			fmt.Fprintln(os.Stderr, "  magus query kind:spell go")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "A single argument shaped like a target-output ref (ref1a2b3c) instead prints")
			fmt.Fprintln(os.Stderr, "that execution's exact captured output to stdout (pipe it anywhere):")
			fmt.Fprintln(os.Stderr, "  magus query ref1a2b3c            print the output")
			fmt.Fprintln(os.Stderr, "  magus query ref1a2b3c --meta     with a project/target/status header")
			fmt.Fprintln(os.Stderr, "  magus query ref1a2b3c --open     open it in the browser log viewer")
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

	// Output-ref routing: a lone positional shaped like a ref id retrieves that
	// execution's captured output. A free-text query like "refactor" (non-hex tail)
	// is NOT a ref and falls through to the graph grammar below.
	if len(pos) == 1 && kinds == "" && cache.LooksLikeRef(pos[0]) {
		return queryOutputRef(ctx, root, pos[0], outputRefOpts{
			meta: meta, open: open, printURL: printURL, viewerBase: viewerURL,
		})
	}
	if open || meta || printURL {
		// A ref-only flag was set, so the visitor meant to name an output ref, but the
		// positional is not a valid ref<hex> - a malformed ref, not a search. Code it so it
		// stops here instead of silently falling through to the graph grammar below.
		msg := "--open/--meta/--print apply only to an output ref (magus query ref<hex>). To open the knowledge graph in a browser, use `magus graph open`."
		fmt.Fprintf(os.Stderr, "magus query: %s\n", types.DiagnosticErrorf(types.OutputRefMalformed, "%s", msg).Error())
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

// outputRefOpts carries the flags that apply when `magus query` is retrieving a
// target-output ref rather than searching the graph.
type outputRefOpts struct {
	meta       bool   // prepend a header before the bytes
	open       bool   // open the browser log viewer instead of printing
	printURL   bool   // with open, print the URL instead of launching a browser
	viewerBase string // log viewer base URL
}

// queryOutputRef retrieves a target's captured output by reference id (or unique
// prefix) and either prints it verbatim to stdout (pipe-friendly) or, with --open,
// hands it to the browser log viewer. The bytes never leave the machine: --open
// rides them in a URL fragment, exactly like `magus graph open`.
func queryOutputRef(ctx context.Context, root, ref string, o outputRefOpts) error {
	m, err := loadMagus(ctx, root)
	if err != nil {
		return err
	}
	if o.open {
		// The viewer ingests a magus.viewer.v1 Journal, so hand it the ref's structured
		// events (not the reconstructed text) - the browser renders pretty from structure.
		events, meta, err := m.OutputEventsByRef(ref)
		if err != nil {
			return reportRefLookupError(ref, err)
		}
		var inv journal.Invocation
		if meta.Inv != "" {
			inv, _ = m.InvocationByID(meta.Inv) // best-effort lineage; omitted if the run log aged out
		}
		return openOutputInViewer(meta, events, inv, o)
	}
	data, meta, err := m.OutputByRef(ref)
	if err != nil {
		return reportRefLookupError(ref, err)
	}
	if o.meta {
		var inv journal.Invocation
		if meta.Inv != "" {
			inv, _ = m.InvocationByID(meta.Inv) // best-effort; lineage omitted if the run log aged out
		}
		writeOutputMetaHeader(os.Stdout, meta, inv)
	}
	_, err = os.Stdout.Write(data)
	return err
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
		msg := fmt.Sprintf("no stored output for ref %q. It may have aged out of the cache, or the ref is mistyped; re-run the target to regenerate it.", ref)
		fmt.Fprintf(os.Stderr, "magus query: %s\n", types.DiagnosticErrorf(types.OutputRefMissing, "%s", msg).Error())
		return errSilent{exitCode: 2}
	default:
		return fmt.Errorf("magus query: look up output ref %q: %w", ref, err)
	}
}

// writeOutputMetaHeader prints the --meta header: the ref, its run's identity, and the
// lineage of the invocation that produced it (the command + trigger, from inv), each on a
// plain line so the block stays greppable and pipe-safe, then a rule. inv may be zero (its
// run log aged out), in which case the lineage lines are omitted.
func writeOutputMetaHeader(w io.Writer, meta cache.OutputMeta, inv journal.Invocation) {
	status := "ok"
	if meta.Failed {
		status = "failed"
	}
	fmt.Fprintf(w, "ref:      %s\n", meta.Ref)
	if meta.Project != "" {
		fmt.Fprintf(w, "project:  %s\n", meta.Project)
	}
	if meta.Target != "" {
		fmt.Fprintf(w, "target:   %s\n", meta.Target)
	}
	fmt.Fprintf(w, "status:   %s\n", status)
	if meta.DurationMs > 0 {
		fmt.Fprintf(w, "duration: %dms\n", meta.DurationMs)
	}
	// Lineage: the command (and trigger) of the invocation that produced this output, so a
	// ref traces back to the run - "which magus command made this, and what set it off".
	if inv.ID != "" {
		cmd := "magus " + inv.Command.Verb
		if len(inv.Command.Args) > 0 {
			cmd += " " + strings.Join(inv.Command.Args, " ")
		}
		fmt.Fprintf(w, "run:      %s\n", cmd)
		if inv.Command.Trigger != "" {
			fmt.Fprintf(w, "trigger:  %s\n", inv.Command.Trigger)
		}
	}
	fmt.Fprintln(w, "----")
}

// buildLogViewerURL assembles the log-viewer deep link: BOTH the ref identity and the encoded
// output ride the URL fragment (after #), which the browser NEVER transmits to a server - so
// nothing about the run, not even its ref id, ever leaves the machine. The payload is a
// magus.viewer.v1 Journal (protobuf, gzip+base64url) of the ref's events; the browser decodes
// it and renders pretty from structure (the generated JS client, bundled in).
func buildLogViewerURL(base string, meta cache.OutputMeta, events []journal.Event, inv journal.Invocation) (link, encoded string, err error) {
	j := journal.InvocationFromEvents(meta.Ref, events)
	// A single ref's persisted events are output+result only (no `started`), so
	// InvocationFromEvents yields no command lineage; graft the resolved run's Command so the
	// viewer's lineage header shows which command (and trigger) produced this output.
	if inv.ID != "" {
		j.Command = inv.Command
	}
	encoded, err = handler.EncodeJournalFragment(j, events)
	if err != nil {
		return "", "", err
	}
	// The encoded blob is returned too so the caller can size-check it without re-parsing the url.
	return strings.TrimRight(base, "/") + "/#ref=" + url.QueryEscape(meta.Ref) + "&data=" + encoded, encoded, nil
}

// openOutputInViewer builds the viewer URL and opens a browser; --print emits the
// URL instead. It warns when the encoded fragment nears browser URL-length limits.
func openOutputInViewer(meta cache.OutputMeta, events []journal.Event, inv journal.Invocation, o outputRefOpts) error {
	openURL, encoded, err := buildLogViewerURL(o.viewerBase, meta, events, inv)
	if err != nil {
		return err
	}
	if len(encoded) > fragmentWarnBytes {
		fmt.Fprintf(os.Stderr, "magus query: this output encodes to %d KB, near or past what Safari and older\n", len(encoded)/1024)
		fmt.Fprintf(os.Stderr, "Firefox accept in a URL (Chrome is fine). If the page does not load, pipe it instead:\n")
		fmt.Fprintf(os.Stderr, "  magus query %s | less. Continuing.\n", meta.Ref)
	}
	if o.printURL {
		fmt.Println(openURL)
		return nil
	}
	fmt.Fprintf(os.Stderr, "opening the log viewer for %s; the output rides in the link fragment and never leaves your machine.\n", meta.Ref)
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

	n := out.Node
	fmt.Printf("node: %s\n", n.ID)
	fmt.Printf("  kind:  %s\n", n.Kind)
	fmt.Printf("  label: %s\n", n.Label)
	if n.Doc != "" {
		fmt.Printf("  doc:   %s\n", n.Doc)
	}
	if n.Source != "" {
		fmt.Printf("  source: %s\n", n.Source)
	}
	for _, k := range sortedKeys(n.Attrs) {
		fmt.Printf("  %s: %s\n", k, n.Attrs[k])
	}
	fmt.Printf("  reached by: %d node(s)\n", out.BlastRadius)
	if len(out.Out) > 0 {
		fmt.Printf("\nout edges (%d):\n", len(out.Out))
		for _, e := range out.Out {
			fmt.Printf("  --%s--> %s  [%s]%s\n", e.Relation, e.Other, e.OtherKind, provSuffix(e.Provenance))
		}
	}
	if len(out.In) > 0 {
		fmt.Printf("\nin edges (%d):\n", len(out.In))
		for _, e := range out.In {
			fmt.Printf("  <--%s-- %s  [%s]%s\n", e.Relation, e.Other, e.OtherKind, provSuffix(e.Provenance))
		}
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

	fmt.Printf("from: %s\n", out.From)
	fmt.Printf("to:   %s\n", out.To)
	if !out.Found {
		fmt.Println("\nno path connects these nodes")
		return nil
	}
	fmt.Printf("\n%s\n", out.From)
	for _, s := range out.Steps {
		if s.Forward {
			fmt.Printf("  --%s--> %s\n", s.Relation, s.To)
		} else {
			fmt.Printf("  <--%s-- %s\n", s.Relation, s.To)
		}
	}
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

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(m))
}

func provSuffix(p string) string {
	if p == "" {
		return ""
	}
	return "  (" + p + ")"
}
