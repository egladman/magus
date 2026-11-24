//go:build buzz_safe || buzz_unsafe

package vm

// The safe and unsafe value representations hold heap objects as ordinary Go
// pointers, so there is no global table to count and none of the pinning the
// nanbox build's accounting exists to diagnose can occur. Everything here but
// the interning counters reports nothing, and the interpreter's sampling hook
// compiles to a no-op.

// HeapStats is a snapshot of the global object heap. This build has no table,
// so only the string counters move: interning pins strings here too.
type HeapStats struct {
	Objects         int
	Peak            int
	InternedStrings int
	InternedBytes   int
	// UnownedObjects is 0: the collector reclaims every object here.
	UnownedObjects int
	// BufferedStrings and BufferedBytes count the buffered strings made and
	// the arrays their buffers allocated. The collector reclaims both here,
	// so they count what was made, not what is held.
	BufferedStrings int
	BufferedBytes   int
}

// ReadHeapStats reports the string counters. The table counts are zero:
// this build has no global heap table.
func ReadHeapStats() HeapStats {
	return HeapStats{
		InternedStrings: int(internedStrings.Load()),
		InternedBytes:   int(internedBytes.Load()),
		BufferedStrings: int(bufferedStrings.Load()),
		BufferedBytes:   int(bufferedBytes.Load()),
	}
}

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

// HeapProfile reports nothing: this build has no global heap table.
func HeapProfile() map[string]int { return nil }

// heapLen reports 0: this build has no global heap table to seed from.
func heapLen() int { return 0 }
