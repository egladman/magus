package main

import (
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
)

// scribes maps a subcommand name to its generator. Each reads a source of truth
// and writes the derived artifact; see the per-subcommand file for the details.
var scribes = map[string]func(args []string) error{
	"types":           runTypes,
	"tstypes":         runTSTypes,
	"boundarylist":    runBoundaryList,
	"boundaryobjects": runBoundaryObjects,
	"boundarydecode":  runBoundaryDecode,
	"contract":        runContract,
	"mcptools":        runMCPTools,
	"jobschema":       runJobSchema,
	"moduledecls":     runModuleDecls,
	"moduleset":       runModuleSet,
	"bindings":        runBindings,
	"libmodules":      runLibModules,
	"completions":     runCompletions,
	"diffdemo":        runDiffDemo,
	"cliflags":        runCLIFlags,
	"config":          runConfig,
	"api":             runAPI,
	"goapi":           runGoAPI,
	"cut":             runCut,
	"dev-shims":       runDevShims,
	"release-index":   runReleaseIndex,
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	sub := os.Args[1]
	run, ok := scribes[sub]
	if !ok {
		fmt.Fprintf(os.Stderr, "magus-utils: unknown subcommand %q\n", sub)
		usage()
		os.Exit(2)
	}
	if err := run(os.Args[2:]); err != nil {
		fmt.Fprintf(os.Stderr, "magus-utils %s: %v\n", sub, err)
		os.Exit(1)
	}
}

// usageLine lists every registered subcommand. Derived from scribes rather than
// written out: the hand-maintained list named 13 of the 20 that existed.
func usageLine() string {
	return fmt.Sprintf("usage: magus-utils <%s> [flags]", strings.Join(slices.Sorted(maps.Keys(scribes)), "|"))
}

func usage() {
	fmt.Fprintln(os.Stderr, usageLine())
}
