//go:build !windows

package main

import (
	"fmt"
	"os"
	"syscall"
)

// bootstrapExecInto replaces this process with target, preserving argv, stdin,
// stdout/stderr and signal delivery for free: syscall.Exec keeps the same pid and
// file descriptors, so a shell, a supervisor, or a script watching this process sees
// no difference from magus having been target all along.
//
// argv[0] becomes target rather than whatever the caller was invoked as, matching
// serverCmd's convention in server.go. The rest of argv (everything after the
// original argv[0]) is passed through unchanged.
//
// execve either fully succeeds (in which case this never returns) or fails without
// having run any of target's code, leaving this process completely intact. So a
// failure here is safe to just report and fall back to normal dispatch under the
// binary already running, which is the same behavior this feature did not exist to
// prevent.
func bootstrapExecInto(target string, argv []string, env []string) {
	execArgv := append([]string{target}, argv[1:]...)
	// gosec reads any non-constant exec target as injection. target is a magus binary
	// path this process resolved for itself, and execing one is the whole function.
	if err := syscall.Exec(target, execArgv, env); err != nil { //nolint:gosec // G702
		fmt.Fprintf(os.Stderr, "magus: exec %s failed: %v\n", target, err)
	}
}
