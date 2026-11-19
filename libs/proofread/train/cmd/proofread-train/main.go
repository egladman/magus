package main

import (
	"context"
	"fmt"
	"io"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
)

func main() {
	command.Main(root())
}

// The process exit codes: 0 for a run that passed, 1 for a calibrate -gates
// run with a rule its cases do not support, 2 for a run that could not finish.
const (
	exitOK         = 0
	exitGateFailed = 1
	exitError      = 2
)

// root is the proofread-train command: train and calibrate, which holds round.
func root() command.Command {
	return command.Command{
		Name:  "proofread-train",
		Short: "train proofread's word-class model and calibrate its rules",
		ExitStatus: []command.ExitCode{
			{Code: exitOK, Meaning: "passed"},
			{Code: exitGateFailed, Meaning: "a rule ships a decision its cases do not support (calibrate --gates)"},
			{Code: exitError, Meaning: "unusable subcommand, flags or input"},
		},
		Children: []command.Command{trainCmd, calibrateCmd},
	}
}

// run returns the process exit code.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return command.Execute(context.Background(), root(), args, command.Streams{Stdin: stdin, Stdout: stdout, Stderr: stderr})
}

// reported turns a status that was already explained on stderr into the error
// that exits with it.
func reported(code int) error {
	if code == exitOK {
		return nil
	}

	return exit.Reported(code)
}

func failure(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "proofread-train: %v\n", err)

	return exitError
}
