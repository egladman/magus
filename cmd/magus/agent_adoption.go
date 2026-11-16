package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/termkit"
)

// agentAdoptionCommand implements `magus agent adoption`: read shell commands (stdin or --commands),
// report how often the graph was used versus grep.
func agentAdoptionCommand(args []string) error {
	fset := flag.NewFlagSet("agent adoption", flag.ContinueOnError)
	bindDisplayFlags(fset)
	af := flaggen.BindAgentAdoption(fset)
	fset.Usage = func() { guard.WriteAdoptionUsage(os.Stderr) }
	if err := fset.Parse(args); err != nil {
		return err
	}
	opts, err := outputOptionsOrDefault()
	if err != nil {
		return err
	}

	var src io.Reader = os.Stdin
	if af.Commands != "" {
		f, err := os.Open(af.Commands)
		if err != nil {
			return fmt.Errorf("agent adoption: %w", err)
		}
		defer f.Close()
		src = f
	} else if termkit.StdinIsTerminal() {
		guard.WriteAdoptionUsage(os.Stderr)
		return exit.Usagef("magus agent adoption: reads commands from stdin or --commands <file>")
	}

	lines, err := guard.ReadAdoptionCommands(src)
	if err != nil {
		return err
	}
	report := guard.AnalyzeAdoption(lines)

	if opts.Format != outputText {
		names := make([]string, len(report.TopSymbolGreps))
		for i, p := range report.TopSymbolGreps {
			names[i] = p.Pattern
		}
		return emitRecord(opts, report, names)
	}
	guard.WriteAdoptionTable(os.Stdout, report)
	return nil
}
