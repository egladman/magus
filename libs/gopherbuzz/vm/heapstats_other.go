//go:build buzz_safe || buzz_unsafe

package vm

// The safe and unsafe value representations hold heap objects as ordinary Go
// pointers, so there is no global table to count and none of the pinning the
// nanbox build's accounting exists to diagnose can occur. Everything here reports
// nothing, and the interpreter's sampling hook compiles to a no-op.

// HeapStats is a snapshot of the global object heap; always zero on this build.
type HeapStats struct {
	Objects int
	Peak    int
}

// ReadHeapStats reports zeroes: this build has no global heap table.
func ReadHeapStats() HeapStats { return HeapStats{} }

// ResetHeapStats does nothing: there is no peak or attribution to rebase.
func ResetHeapStats() {}

// HeapSite is one source position and the heap growth attributed to it.
type HeapSite struct {
	Site    string
	Objects int64
}

// HeapHotSites reports nothing: this build has no heap growth to attribute.
func HeapHotSites(int) ([]HeapSite, bool) { return nil, false }

// heapSampleMask is set so the interpreter's masked check never fires.
const heapSampleMask = ^uint64(0)

// sampleHeapGrowth is a no-op; the call site is unreachable given heapSampleMask.
func sampleHeapGrowth(*frame, *int) {}
