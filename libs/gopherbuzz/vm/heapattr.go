//go:build !buzz_safe && !buzz_unsafe

package vm

import (
	"sort"
	"sync"
)

// Heap growth attribution: which source line is filling the heap.
//
// HeapStats says the heap is enormous. It does not say who did it, and on a
// magusfile of any size that leaves the reader bisecting. This records the source
// position responsible so the diagnostic can name a line instead of a suspicion.
//
// Sampled from the interpreter loop rather than from gHeapAlloc, for two reasons.
// The allocator has no frame to attribute to - it is a package function reached
// from every value constructor - while the loop already holds one. And the
// allocator is the hottest path in the VM, where a review has already objected to
// a single atomic load; the loop can carry a masked counter instead.
//
// Sampling means the numbers are proportions, not totals, and the position is the
// LOOP rather than the exact allocating statement. A sample lands on whichever
// instruction the tick caught, and in a tight loop the loop control executes as
// often as the body, so a growing `foreach` reports its own header line more often
// than the concatenation inside it.
//
// That is the useful answer anyway. "The loop at coverage.buzz:88 is filling the
// heap" puts the reader on the right five lines; pinning the exact statement would
// mean attributing at allocation time, which is the hot path this deliberately
// avoids.

const (
	// heapSampleMask makes the check fire every 4096 instructions. A single AND
	// and a not-taken branch on the other 4095.
	heapSampleMask = 4095

	// heapAttrMaxSites bounds the table. A runaway loop concentrates on a handful
	// of lines; a program that touches thousands of distinct sites at this
	// granularity has no hot spot to report anyway, and an unbounded map here
	// would be its own leak.
	heapAttrMaxSites = 256
)

var heapAttr struct {
	mu sync.Mutex
	// growth maps "source:line" to heap objects added while that line was
	// executing, as observed at sample points.
	growth map[string]int64
	// full records that the table hit its bound, so a report can say it is
	// partial rather than quietly present a truncated ranking as complete.
	full bool
}

// sampleHeapGrowth attributes heap growth since the last sample to f's current
// position. Called from the interpreter loop on a masked counter.
func sampleHeapGrowth(f *frame, lastLen *int) {
	s := gHeapPtr.Load()
	if s == nil {
		return
	}
	n := len(*s)
	grown := n - *lastLen
	*lastLen = n
	if grown <= 0 || f == nil || f.chunk == nil {
		return
	}
	// Drop a sample with no line information. A chunk compiled without line data
	// yields "<main>:?", which names nothing a reader can open, and on a runner it
	// outranked the actual growing loop - a ranking led by an unattributable entry
	// is worse than a shorter honest one.
	line := f.chunk.lineAt(f.ip - 1)
	if line <= 0 {
		return
	}
	site := f.chunk.Name
	if site == "" {
		site = "<buzz>"
	}
	site += ":" + itoa(line)

	heapAttr.mu.Lock()
	defer heapAttr.mu.Unlock()
	if heapAttr.growth == nil {
		heapAttr.growth = make(map[string]int64, 32)
	}
	if _, known := heapAttr.growth[site]; !known && len(heapAttr.growth) >= heapAttrMaxSites {
		heapAttr.full = true
		return
	}
	heapAttr.growth[site] += int64(grown)
}

// HeapSite is one source position and the heap growth attributed to it.
type HeapSite struct {
	// Site is "source:line".
	Site string
	// Objects is heap objects added while this line ran, as sampled.
	Objects int64
}

// HeapHotSites returns the source positions responsible for the most heap growth,
// largest first, at most n of them, plus whether the attribution table filled up
// and so may be missing sites.
//
// Empty when nothing has been sampled, which is the normal case for a short run.
// Callers should treat a nil result as "no attribution available" and fall back to
// the bare object count from HeapStats.
func HeapHotSites(n int) (sites []HeapSite, partial bool) {
	heapAttr.mu.Lock()
	defer heapAttr.mu.Unlock()
	if len(heapAttr.growth) == 0 {
		return nil, heapAttr.full
	}
	sites = make([]HeapSite, 0, len(heapAttr.growth))
	for site, objects := range heapAttr.growth {
		sites = append(sites, HeapSite{Site: site, Objects: objects})
	}
	// Ties broken by site so the ranking is stable across runs; a diagnostic that
	// reorders equal entries reads as though something changed.
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].Objects != sites[j].Objects {
			return sites[i].Objects > sites[j].Objects
		}
		return sites[i].Site < sites[j].Site
	})
	if n > 0 && len(sites) > n {
		sites = sites[:n]
	}
	return sites, heapAttr.full
}

// itoa formats a positive line number without pulling in strconv on this path.
func itoa(n int) string {
	if n <= 0 {
		return "?"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// resetHeapAttr clears the growth table. See ResetHeapStats.
func resetHeapAttr() {
	heapAttr.mu.Lock()
	defer heapAttr.mu.Unlock()
	heapAttr.growth = nil
	heapAttr.full = false
}
