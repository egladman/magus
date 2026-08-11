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
// statistics. allocated() returns the current HeapInuse from runtime.MemStats
// as the closest approximation of "bytes presently allocated".
func gcModule() vm.Value {
	m := mod()
	m.MapSet("allocated", fn("gc.allocated", gcAllocated))
	m.MapSet("collect", fn("gc.collect", gcCollect))
	return m
}

func gcAllocated(_ context.Context, _ []vm.Value) (vm.Value, error) {
	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)
	return vm.IntValue(int64(ms.HeapInuse)), nil
}

func gcCollect(_ context.Context, _ []vm.Value) (vm.Value, error) {
	goruntime.GC()
	// Buzz's collect() can throw CollectError; Go's GC() never fails.
	return vm.Null, nil
}
