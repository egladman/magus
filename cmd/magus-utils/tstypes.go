package main

import (
	"flag"
	"fmt"

	"github.com/egladman/magus/internal/generate/emit"
)

// runTSTypes writes the TypeScript form of every boundary type flagged TS to each -out.
// The console and docs bundle separately, so each gets its own copy.
func runTSTypes(args []string) error {
	fs := flag.NewFlagSet("tstypes", flag.ExitOnError)
	var outs []string
	fs.Func("out", "output .ts file path (repeatable)", func(s string) error {
		outs = append(outs, s)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(outs) == 0 {
		return fmt.Errorf("usage: magus-utils tstypes -out <path.ts> [-out <path.ts>...]")
	}
	src, err := renderTSTypes(boundaryTypes())
	if err != nil {
		return err
	}
	for _, out := range outs {
		if err := emit.File(out, src); err != nil {
			return fmt.Errorf("write %s: %w", out, err)
		}
	}
	return nil
}

// renderTSTypes renders the TS-flagged entries in registry order.
func renderTSTypes(entries []boundaryType) ([]byte, error) {
	var ts []emit.TSType
	for _, e := range entries {
		if e.TS {
			ts = append(ts, emit.TSType{Name: e.Name, Type: e.Type})
		}
	}
	return emit.TSTypes(ts, buzzName)
}
