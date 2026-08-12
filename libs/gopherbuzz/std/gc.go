package std

import (
	"context"
	goruntime "runtime"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// gcModule builds the "gc" module matching Buzz's gc reference:
// https://buzz-lang.dev/0.5.0/reference/std/gc.html
//
// The Go runtime does not expose a 1:1 equivalent of Buzz's Zig allocator
// statistics. allocated() reports HeapAlloc - the bytes of LIVE heap objects - which
// is the closest analogue to upstream's "presently allocated" and, unlike HeapInuse,
// actually falls after a collection. HeapInuse counts whole spans the runtime has
// reserved and rarely returns, so it could report growth immediately after a
// successful collect.
func gcModule() vm.Value {
	m := mod()
	m.MapSet("allocated", fn("gc.allocated", gcAllocated))
	m.MapSet("collect", fn("gc.collect", gcCollect))
	return m
}

func gcAllocated(_ context.Context, _ []vm.Value) (vm.Value, error) {
	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)
	return vm.IntValue(int64(ms.HeapAlloc)), nil
}

func gcCollect(ctx context.Context, _ []vm.Value) (vm.Value, error) {
	// The callback sweep runs FIRST: it walks the VM's roots and allocates while
	// doing so, and running Go's GC afterwards is what keeps that bookkeeping from
	// showing up as growth in the allocated() a caller reads next.
	//
	// This is the part Go's GC cannot do on its own. Reachability is computed over
	// the VM's own roots, because a Go finalizer arrives on another goroutine at an
	// unspecified time and this VM is single-goroutine - see vm/gc_collect.go.
	if running := vm.FromContext(ctx); running != nil {
		if _, err := running.CollectUnreachable(); err != nil {
			return vm.Null, err
		}
	}
	goruntime.GC()
	return vm.Null, nil
}
