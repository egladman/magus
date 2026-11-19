package main

import (
	"github.com/egladman/magus/benchmarks/agent/internal/bench"
	"github.com/egladman/magus/libs/clikit/command"
)

func main() { command.Main(bench.ReportCommand()) }
