package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/internal/workspace"
)

// maybeBootstrapExec looks for a workspace-local ./magus and, when one is found and
// is not the binary already running, replaces this process with it before ANYTHING
// else happens: argv is not yet parsed, no config is loaded, and no workspace has
// been opened. That ordering is the entire point: the released magus on PATH can sit
// below a workspace's required_version floor, which means it cannot load the
// workspace far enough to even discover its own floor problem, so the escape has to
// work on nothing but a directory walk.
//
// Never returns when it execs: bootstrapExecInto either replaces the process image
// (unix) or runs the target to completion and exits with its code (windows); see
// bootstrap_exec_unix.go / bootstrap_exec_windows.go. A caller needs no else branch.
func maybeBootstrapExec(argv []string) {
	// testing.Testing() is true for the go-test-compiled binary AND for every copy
	// testscript.Main spawns as a script subprocess (copyBinary duplicates the same
	// test binary bytes, which keep the linked-in test flag). That one guard keeps
	// every existing runCLI()-driven test (the in-process runCLIQuietly helper in
	// globals_test.go and testscript's own subprocess "exec magus" alike) unaffected
	// by this worktree's own real ./magus sitting a few directories up. Without it,
	// running the test suite from inside this tree would replace the test process
	// itself with that binary. bootstrap_exec_test.go exercises the real mechanism
	// through a separately-spawned, non-test fixture process instead, which correctly
	// is not caught by this guard.
	if testing.Testing() {
		return
	}
	self, err := runningBinaryPath()
	// The bootstrap builds ./magus with `go run`, so that build is the fresh
	// one and the placed ./magus the stale one.
	if err != nil || ward.GoRunBinary(self) {
		return
	}
	target, ok := workspace.BootstrapExecTarget(extractRootFlag(argv[1:]), self)
	if !ok {
		return
	}
	if why := bootstrapExecRefusal(target, self); why != "" {
		fmt.Fprintf(os.Stderr, "magus: not using %s: %s\n", target, why)
		return
	}
	// Before log setup: the exec replaces this process ahead of any display.
	fmt.Fprintf(os.Stderr, "magus: using this workspace's own binary at %s instead of %s\n", target, self)
	env := append(os.Environ(), workspace.BootstrapExecSentinelVar+"=1")
	bootstrapExecInto(target, argv, env)
}

// bootstrapExecRefusal says why a checkout's own ./magus must not hand over
// to target, or "" when it may. The glue runs it wherever a call lands, so
// only a checkout of its repository takes over. An installed binary defers
// anywhere.
func bootstrapExecRefusal(target, self string) string {
	session := binaryCheckout(self)
	if session == "" {
		return ""
	}
	return foreignCheckout(filepath.Dir(target), session)
}
