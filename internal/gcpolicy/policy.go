package gcpolicy

import (
	"os"
	"runtime/debug"
)

// Percent is the GOGC this package's init sets unless GOGC is in the environment.
//
// optimization: raise GOGC during package init, before the heap reaches the 4MB minimum.
//
//	measured: gctrace: the one GC before main is gone on `magus version` and both hooks.
//	          hyperfine, 600 runs per arm, arms alternating: version 18.6->17.0ms, commit-msg
//	          hook 58.8->57.4ms, tool-call hook 72.6->70.6ms mean wall; about 3ms less CPU each.
//	trade-off: the linker places this init after about 1MB of other packages' init.
//	assumes:  darwin arm64, 10 P; cmd/magus's relaxStartupGC owns the restore.
const Percent = 400

var (
	prior  int
	raised bool
)

func init() {
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
