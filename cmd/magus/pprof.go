package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
)

// MAGUS_PPROF writes a Go profile for one magus invocation:
//
//	MAGUS_PPROF=cpu:/tmp/magus.cpu.pprof   magus run site-generate docs
//	MAGUS_PPROF=mem:/tmp/magus.mem.pprof   magus run site-generate docs
//	MAGUS_PPROF=cpu:/tmp/c,mem:/tmp/m      both, comma separated
//
// Read straight from the environment rather than added to Config, and deliberately
// NOT a CLI flag. It is a diagnostic aimed at whoever is working on magus itself, so
// it does not belong in the generated configuration reference next to the keys users
// are meant to set, and it does not belong in --help next to the flags they are meant
// to reach for. The MAGUS_ prefix keeps it discoverable in the one place it matters:
// the environment of a slow run.
//
// A CPU profile attributes straight through to the Buzz VM's opcode handlers, since
// the interpreter runs in this process - which is what makes it the right tool for a
// slow magusfile, not just for slow Go. A `mem` profile is the heap at exit, which is
// what to reach for when a run's `sys` time is dominated by allocator churn rather
// than by work.
//
// Failures here are reported and then ignored: a mistyped profile spec must not fail
// the build the developer was actually trying to measure.
const pprofEnv = "MAGUS_PPROF"

// startProfiling honors MAGUS_PPROF and returns a stop function. The stop function is
// always non-nil, so callers can defer it unconditionally.
func startProfiling() func() {
	spec := strings.TrimSpace(os.Getenv(pprofEnv))
	if spec == "" {
		return func() {}
	}
	var stops []func()
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kind, path, ok := strings.Cut(part, ":")
		if !ok {
			fmt.Fprintf(os.Stderr, "magus: %s: want <cpu|mem>:<path>, got %q\n", pprofEnv, part)
			continue
		}
		if stop := startOneProfile(strings.TrimSpace(kind), strings.TrimSpace(path)); stop != nil {
			stops = append(stops, stop)
		}
	}
	return func() {
		for _, stop := range stops {
			stop()
		}
	}
}

// startOneProfile starts a single named profile, or reports why it could not and
// returns nil.
func startOneProfile(kind, path string) func() {
	if path == "" {
		fmt.Fprintf(os.Stderr, "magus: %s: %s profile needs an output path\n", pprofEnv, kind)
		return nil
	}
	// A profile written into the workspace would be an undeclared output that the
	// sandbox and the drift gate both have opinions about, so the parent directory is
	// created but the path is otherwise the caller's problem.
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "magus: %s: %v\n", pprofEnv, err)
			return nil
		}
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "magus: %s: %v\n", pprofEnv, err)
		return nil
	}

	switch kind {
	case "cpu":
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "magus: %s: %v\n", pprofEnv, err)
			f.Close()
			return nil
		}
		fmt.Fprintf(os.Stderr, "magus: cpu profile -> %s\n", path)
		return func() {
			pprof.StopCPUProfile()
			f.Close()
		}
	case "mem":
		fmt.Fprintf(os.Stderr, "magus: mem profile -> %s\n", path)
		return func() {
			// GC first so the heap profile describes what is genuinely retained at
			// exit rather than whatever had not been collected yet.
			runtime.GC()
			if err := pprof.WriteHeapProfile(f); err != nil {
				fmt.Fprintf(os.Stderr, "magus: %s: %v\n", pprofEnv, err)
			}
			f.Close()
		}
	default:
		fmt.Fprintf(os.Stderr, "magus: %s: unknown profile %q (want cpu or mem)\n", pprofEnv, kind)
		f.Close()
		return nil
	}
}
