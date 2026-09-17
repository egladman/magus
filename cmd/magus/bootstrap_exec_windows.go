//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
)

// bootstrapExecInto is the windows counterpart to the unix syscall.Exec form: there
// is no exec() syscall to replace this process's image with, so this spawns target as
// a child, wires its stdio straight through, waits for it, and exits with its exact
// code. argv and stdio match the unix build; the exit code matches for a target that
// runs to completion. Signal delivery is the one thing this cannot give for free -
// windows has no POSIX signal delivery to a specific process the way unix does - so a
// Ctrl+C reaches the child through its own console handling rather than being
// forwarded by this process.
//
// cmd.Start failing (target missing, not runnable) is the same "safe to fall back"
// case as a unix exec failure: nothing of target has run yet, so this reports it and
// returns, leaving normal dispatch to proceed under the binary already running.
// cmd.Wait failing means target DID run, so its result - not a fallback that would
// risk doing the work twice - is what this process exits with.
func bootstrapExecInto(target string, argv []string, env []string) {
	cmd := exec.Command(target, argv[1:]...)
	cmd.Env = env
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "magus: exec %s failed: %v\n", target, err)
		return
	}
	err := cmd.Wait()
	if err == nil {
		os.Exit(0)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		os.Exit(exitErr.ExitCode())
	}
	fmt.Fprintf(os.Stderr, "magus: %s: %v\n", target, err)
	os.Exit(1)
}
