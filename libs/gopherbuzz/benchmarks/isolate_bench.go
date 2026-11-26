package benchmarks

import "runtime"

// workSource is CPU-bound and allocation-light, so wall time scaling reflects
// the VM and not the garbage collector.
const workSource = `
fun recurse(n: int) > int {
    if (n < 2) {
        return n;
    }
    return recurse(n - 1) + recurse(n - 2);
}

final total = recurse(24);
`

func heapInUse() int64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return int64(m.HeapAlloc)
}
