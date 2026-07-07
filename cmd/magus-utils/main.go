// Command magus-utils transcribes magus's sources of truth into their derived
// artifacts so the two can never drift. It is a thin dispatcher over one
// subcommand per source→artifact mapping, invoked by `go generate`:
//
//	//go:generate go run ../../cmd/magus-utils types -type Target -out buzzlib/target.gen.buzz
//	//go:generate go run ../cmd/magus-utils bindings -module fs -lang buzz -out ../host/gen/fs.go
//	//go:generate go run ../magus-utils config -config ../../internal/config/config.go -out gen/config_flags.go
//	//go:generate go run ../../cmd/magus-utils spells -spells ../../spells -out gen
//
// Each subcommand reads a Go or Buzz source of truth and emits its mirror; none
// is ever linked into the magus binary.
package main

import (
	"fmt"
	"os"
)

// scribes maps a subcommand name to its generator. Each reads a source of truth
// and writes the derived artifact; see the per-subcommand file for the details.
var scribes = map[string]func(args []string) error{
	"types":    runTypes,
	"bindings": runBindings,
	"config":   runConfig,
	"spells":   runSpells,
	"sign":     runSign,
	"api":      runAPI,
	"verify":   runVerify,
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

func usage() {
	fmt.Fprintln(os.Stderr, "usage: magus-utils <types|bindings|config|spells|sign|api|verify> [flags]")
}
