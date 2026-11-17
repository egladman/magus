package gcpolicy

import (
	"os"
	"runtime/debug"
	"sync"
	"time"
)

// Percent is the GOGC this package's init sets unless GOGC is in the environment.
//
// optimization: raise GOGC during package init, before the heap reaches the 4MB minimum.
//
//	measured: gctrace: the one GC before main is gone on `magus version` and both hooks.
//	          hyperfine, 600 runs per arm, arms alternating: version 18.6->17.0ms, commit-msg
//	          hook 58.8->57.4ms, tool-call hook 72.6->70.6ms mean wall; about 3ms less CPU each.
//	trade-off: the linker places this init after about 1MB of other packages' init.
//	assumes:  darwin arm64, 10 P; RelaxStartup owns the restore.
const Percent = 400

var (
	prior  int
	raised bool
)

func init() { //nolint:gochecknoinits // measured: GC must be raised before the heap reaches 4 MB, which is before main; see the optimization block
	if os.Getenv("GOGC") != "" {
		return
	}
	prior = debug.SetGCPercent(Percent)
	raised = true
}

// Prior returns the GOGC init replaced, or false when an explicit GOGC kept init from
// raising it. Nothing in this package restores it.
func Prior() (percent int, ok bool) {
	return prior, raised
}

// StartupWindow is how long RelaxStartup holds the raise. Evaluating every magusfile and
// local spell allocates heavily and retains little: measured on this repository, 400
// cut `magus ls` from 121ms to 110ms and its CPU by a quarter, for 37MB more peak RSS.
//
// optimization: run the first second of every process under Percent, not only the load.
//
//	measured: median wall, interleaved runs, n=60: commit-msg hook 74.1->61.6ms, tool-call hook
//	          89.7->78.1ms, describe targets 240.8->233.9ms; one fewer GC each.
//	trade-off: a long-lived process grows its heap 5x live for its first second.
//	assumes:  darwin arm64, 10 P; the GCs a short process pays are the 4MB-minimum-heap ones.
//
// GC off under a 512MiB limit ran level with 400 and peaked 100MB higher on describe targets.
// GOGC 50, 100 and 200, GOMAXPROCS 2 and 4, and a freed 64 to 256MB ballast all lost to 400.
const StartupWindow = time.Second

// RelaxStartup holds Relax for window, so a command that exits sooner never restores it.
// It takes over init's raise, restoring the GOGC that raise replaced.
func RelaxStartup(window time.Duration) {
	restore := Relax()
	if prior, ok := Prior(); ok {
		relaxMu.Lock()
		if relaxCount == 1 && relaxPrev == Percent {
			relaxPrev = prior
		}
		relaxMu.Unlock()
	}
	time.AfterFunc(window, restore)
}

var (
	relaxMu    sync.Mutex
	relaxCount int
	relaxPrev  int
)

// Relax raises GOGC for a workspace load and returns the restore. An explicit GOGC in
// the environment is the caller's choice and is left alone.
//
// Refcounted and shared across callers because loadMagus and inspectWorkspace can both
// first-load concurrently (the server bootstraps both in parallel goroutines). A plain
// save/restore pair races there: either could observe the other's already-raised GOGC
// as "the prior value" and restore to it instead of the true original, or one
// restoring early could drop GOGC out from under the other's still-running load. Only
// the caller that takes the count from 0 saves the prior value; only the one that takes
// it back to 0 restores it.
func Relax() func() {
	if os.Getenv("GOGC") != "" {
		return func() {}
	}
	relaxMu.Lock()
	if relaxCount == 0 {
		relaxPrev = debug.SetGCPercent(Percent)
	}
	relaxCount++
	relaxMu.Unlock()
	return func() {
		relaxMu.Lock()
		relaxCount--
		if relaxCount == 0 {
			debug.SetGCPercent(relaxPrev)
		}
		relaxMu.Unlock()
	}
}
