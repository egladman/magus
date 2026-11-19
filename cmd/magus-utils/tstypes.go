package main

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
)

// tsTypesCommand writes the TypeScript form of every boundary type flagged TS to each -out.
// The console and docs bundle separately, so each gets its own copy.
func tsTypesCommand() command.Command {
	var outs []string
	return command.Command{
		Name:  "tstypes",
		Short: "Write the TypeScript form of the boundary types",
		Flags: []command.Flag{
			{Name: "out", Kind: command.StringList, Doc: "output .ts file path (repeatable)", Target: &outs},
		},
		Run: func(context.Context, command.Invocation) error {
			if len(outs) == 0 {
				return exit.Usagef("usage: magus-utils tstypes -out <path.ts> [-out <path.ts>...]")
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
		},
	}
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
