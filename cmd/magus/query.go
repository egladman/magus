package main

import (
	"context"
	"flag"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/types"
)

// query/explain/path are the knowledge-graph retrieval verbs. They reuse
// prior-art vocabulary (graph tooling generally) and sit on the same cache-first
// substrate as `magus graph export`. query resolves terms to nodes and returns
// the neighborhood; explain shows one node's context; path connects two nodes.

func queryCmd(ctx context.Context, root string, args []string) error {
	var (
		budget      int
		kinds       string
		refresh     bool
		globalScope bool
	)
	pos, err := cmdParse("query", args, func(fs *flag.FlagSet) {
		fs.IntVar(&budget, "budget", 0, "max nodes in the returned neighborhood (default 50)")
		fs.StringVar(&kinds, "kind", "", "restrict matches to these node kinds (comma-separated)")
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before querying")
		fs.BoolVar(&globalScope, "global", false, "query across the workspaces registered in config (knowledge.workspaces); IDs are namespaced by workspace")
		fs.Usage = func() {
			fmt.Fprintln(os.Stderr, "Usage: magus query <terms> [flags]")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, types.KnowledgeQueryDefinition)
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Terms are free text plus field filters: kind:spell, project:pkg/foo,")
			fmt.Fprintln(os.Stderr, "relation:uses, id:build, and negation (-kind:op). Example:")
			fmt.Fprintln(os.Stderr, "  magus query kind:spell go")
			fmt.Fprintln(os.Stderr, "")
			fmt.Fprintln(os.Stderr, "Flags (global flags also accepted, see `magus -h`):")
			fs.PrintDefaults()
		}
	})
	if err != nil {
		return err
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
