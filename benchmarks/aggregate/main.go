package main

import (
	"context"

	"github.com/egladman/magus/benchmarks/aggregate/summary"
	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
)

func main() { command.Main(root()) }

func root() command.Command {
	return command.Command{
		Name:     "aggregate",
		Short:    "Summarize a benchmark results directory as Markdown and a CSV.",
		Usage:    "aggregate <results-dir>",
		Operands: []command.Operand{{Name: "results-dir", Short: "the directory of result JSON files"}},
		Examples: []command.Example{
			{Comment: "Summarize a run", Command: "aggregate benchmarks/results"},
		},
		Run: func(_ context.Context, inv command.Invocation) error {
			if len(inv.Args) != 1 {
				return exit.Usagef("want exactly one results directory, got %d", len(inv.Args))
			}
			return summary.Summarize(inv.Stdout, inv.Stderr, inv.Args[0])
		},
	}
}
