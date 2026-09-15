package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// bootstrapExecSentinelVar marks a process that already replaced itself with a
// workspace-local ./magus via maybeBootstrapExec, so a second hop can never chain.
//
// Distinct from run.AncestorsEnvVar / MAGUS_LEVEL (internal/proc/run/exec.go): those
// describe a magus process spawning ANOTHER magus as a child invocation, i.e.
// recursion depth. This is not that: the exec below replaces THIS process's image
// with the same argv, stdio and exit code, so it is one invocation continuing under a
// different binary, not a new, nested one. Reusing MAGUS_LEVEL would misreport a
// straight-line bootstrap substitution as recursion to everything keyed on depth
// (admission, telemetry, the "topLevel" checks in startup()), so this gets its own
// var instead, kept in the same MAGUS_* namespace.
const bootstrapExecSentinelVar = "MAGUS_BOOTSTRAP_EXEC_DONE"

// bootstrapExecOptOutVar disables maybeBootstrapExec entirely. Documented in
// internal/config/config.go's EnvVarDocs alongside the rest of the MAGUS_* surface.
const bootstrapExecOptOutVar = "MAGUS_NO_BOOTSTRAP_EXEC"

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
	if err != nil {
		return
	}
	target, ok := bootstrapExecDecision(argv, self)
	if !ok {
		return
	}
	fmt.Fprintf(os.Stderr, "magus: using this workspace's own binary at %s instead of %s\n", target, self)
	env := append(os.Environ(), bootstrapExecSentinelVar+"=1")
	bootstrapExecInto(target, argv, env)
}

// bootstrapExecDecision is maybeBootstrapExec's decision logic without its test-binary
// guard or side effects (the stderr line, the actual exec), so bootstrap_exec_test.go
// can drive every branch (the opt-out, the sentinel, --root vs $PWD, no magusfile.buzz
// anywhere above) directly, without needing a real, non-test process to do it in.
func bootstrapExecDecision(argv []string, self string) (target string, ok bool) {
	if truthyEnv(os.Getenv(bootstrapExecOptOutVar)) {
		return "", false
	}
	if os.Getenv(bootstrapExecSentinelVar) != "" {
		return "", false
	}

	start := extractRootFlag(argv[1:])
	if start == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", false
		}
		start = wd
	}
	start, err := filepath.Abs(start)
	if err != nil {
		return "", false
	}

	return resolveBootstrapExecTarget(start, self)
}

// resolveBootstrapExecTarget walks up from start looking for the nearest ancestor
// directory that declares magusfile.buzz (exactly what the shell guard template at
// docs/guides/integrations/agents/guard-templates.md does, matched deliberately
// rather than reinvented) and stops at the FIRST one found.
//
// Stopping there, rather than continuing further up, is what keeps this from ever
// crossing out of the tree start is already inside: a nested worktree (this
// repository keeps one under .claude/worktrees/) declares its OWN magusfile.buzz at
// its own root, so the walk halts there and never reaches a parent checkout's copy
// above it. By construction the only directory this can ever resolve a binary from is
// the nearest project root above start, never an unrelated tree beside or above it.
//
// Returns ok=false when no such directory exists, it exists but has no usable
// ./magus, or that binary resolves to self.
func resolveBootstrapExecTarget(start, self string) (target string, ok bool) {
	dir := start
	for {
		if info, err := os.Stat(filepath.Join(dir, "magusfile.buzz")); err == nil && !info.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}

	name := "magus"
	if runtime.GOOS == "windows" {
		name = "magus.exe"
	}
	candidate := filepath.Join(dir, name)
	info, err := os.Stat(candidate)
	if err != nil || info.IsDir() {
		return "", false
	}
	// Windows has no execute-permission bit; a stat that resolved is enough given the
	// name already carries the platform's executable extension.
	if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		return "", false
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	if resolved == self {
		return "", false
	}
	return resolved, true
}

// truthyEnv matches the 1/true/yes convention MAGUS_NO_WAIT (lock.go's noWaitLocks)
// and MAGUS_SANDBOX_ENABLED already use for a boolean MAGUS_* env var.
func truthyEnv(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}
