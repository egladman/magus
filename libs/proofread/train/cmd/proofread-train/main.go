package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// The process exit codes: 0 for a run that passed, 1 for a calibrate -gates
// run with a rule its cases do not support, 2 for a run that could not finish.
const (
	exitOK         = 0
	exitGateFailed = 1
	exitError      = 2
)

// run returns the process exit code.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage())

		return exitError
	}

	switch args[0] {
	case "train":
		return runTrain(args[1:], stdout, stderr)
	case "calibrate":
		return runCalibrate(args[1:], stdin, stdout, stderr)
	}

	fmt.Fprintf(stderr, "proofread-train: unknown subcommand %q\n\n%s", args[0], usage())

	return exitError
}

func usage() string {
	var b strings.Builder

	b.WriteString("usage: proofread-train <subcommand> [flags]\n\nsubcommands:\n")
	b.WriteString("  train            train the word-class model on UD English EWT into --model, its test split into --testdata\n")
	b.WriteString("  calibrate        replay the labeled cases and print each rule's precision and recall\n")
	b.WriteString("  calibrate round  sample, freeze or score a labeling round, or pool two rounds to decide a deny\n")
	b.WriteString("\ntrain flags: --model, --testdata, --iterations, --seed, --levels\n")
	b.WriteString("calibrate flags: --cases, -o, --gates, --sample, --kind, --field, --group\n")
	b.WriteString("\nExit codes: 0 passed, 1 a rule ships a decision its cases do not support (calibrate --gates), ")
	b.WriteString("2 unusable subcommand, flags or input.\n")

	return b.String()
}

func failure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "proofread-train: %v\n", err)

	return exitError
}
