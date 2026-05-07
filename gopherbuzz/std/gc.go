package std

import (
	"context"
	"fmt"
	goruntime "runtime"

	buzz "github.com/egladman/gopherbuzz"
)

// gcModule builds the "gc" module matching Buzz's gc reference:
// https://buzz-lang.dev/0.5.0/reference/std/gc.html
//
// The Go runtime does not expose a 1:1 equivalent of Buzz's Zig allocator
// statistics. allocated() returns the current HeapInuse from runtime.MemStats
// as the closest approximation of "bytes presently allocated".
func gcModule() buzz.Value {
	m := mod()
	m.MapSet("allocated", fn("gc.allocated", gcAllocated))
	m.MapSet("collect", fn("gc.collect", gcCollect))
	return m
}

func gcAllocated(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
	var ms goruntime.MemStats
	goruntime.ReadMemStats(&ms)
	return buzz.IntValue(int64(ms.HeapInuse)), nil
}

func gcCollect(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
	goruntime.GC()
	// Buzz's collect() can throw CollectError; Go's GC() never fails.
	if false {
		return buzz.Null, fmt.Errorf("gc.collect: collection failed") // satisfy type checker
	}
	return buzz.Null, nil
}
