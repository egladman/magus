//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// bootstrapExecInto runs target as a child with this process's stdio and exits with its
// code, since windows has no exec() to replace this image. A start failure ran nothing
// of target, so it is reported and normal dispatch proceeds; Ctrl+C reaches the child
// through its own console rather than being forwarded.
func bootstrapExecInto(target string, argv []string, env []string) {
	cmd := exec.Command(target, argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "magus: exec %s failed: %v\n", target, err)
		return
	}
	os.Exit(childExitCode(target, cmd.Wait()))
}

// childExitCode is the code to exit with once target ran: its own, or 1 when the wait
// failed without one. target did run, so falling back would risk doing its work twice.
func childExitCode(target string, err error) int {
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exitErr):
		return exitErr.ExitCode()
	}
	fmt.Fprintf(os.Stderr, "magus: %s: %v\n", target, err)
	return 1
}
