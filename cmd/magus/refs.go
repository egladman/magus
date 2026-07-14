package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/types"
)

// refsCmd implements `magus refs <symbol>`: list where an ingested code symbol is
// defined and every file that references it. refs is always symbol-seeded, so it
// loads the lazily-loaded @symbols shards. Its output is occurrence-shaped (file:line
// rows), which is why it is a distinct verb rather than a `magus query` neighborhood.
func refsCmd(ctx context.Context, root string, args []string) error {
	var refresh bool
	pos, err := cmdParse("refs", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&refresh, "refresh", false, "force a full graph rebuild before resolving")
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

	g, err := loadKnowledgeGraphForRefs(ctx, root, refresh, pos[0])
	if err != nil {
		return err
	}
	out, ok := g.Refs(pos[0])
	if !ok {
		// Distinguish the two failure modes: with no symbol index built at all,
		// nothing could ever match, so point at how to build one instead of implying
		// the symbol name is wrong.
		if !g.HasSymbols() {
			fmt.Fprintf(os.Stderr, "magus refs: no symbol index has been built, so there are no symbols to match %q\n", pos[0])
			fmt.Fprintf(os.Stderr, "build one with `%s`; the daemon's auto-indexer also keeps it current while `%s` runs\n", clihint.GraphBuild, clihint.ServerStart)
			return errSilent{exitCode: 2}
		}
		fmt.Fprintf(os.Stderr, "magus refs: no node matches %q\n", pos[0])
		return errSilent{exitCode: 2}
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
		return nil
	}
	fmt.Printf("referenced in %d file(s), %d occurrence(s):\n", out.FileCount, out.RefCount)
	for _, r := range out.Refs {
		fmt.Printf("  %s  (%d)%s\n", r.File, r.Count, linesSuffix(r.Lines))
	}
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
