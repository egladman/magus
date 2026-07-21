package std

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// bufferModule builds the "buffer" module matching Buzz's buffer reference:
// https://buzz-lang.dev/0.5.0/reference/std/buffer.html
//
// Buffer is a mutable byte container. Each instance is modelled as a buzz map
// whose methods close over a shared *bufferState — the same closure pattern used
// for File in io.go.
//
// Two independent APIs share the object, matching upstream Buzz's Buffer:
//
//   - The string/byte API (write/read/at/toString/split/…) operates on a Go-heap
//     growing slice (state.bytes).
//   - The Zig/pointer API (writeZAt/readZAt/ptr) operates on a pinned FFI block at
//     a stable machine address, so the address from ptr() can be handed to a C
//     function declared via zdef that fills it as an out-parameter — the same
//     pinned-memory provider that backs the ffi std module (vm/ffi_mem.go). The
//     block is allocated lazily on first Zig/pointer use, sized to the buffer's
//     capacity, and released by collect().
//
// upstream's writeZ/readZ use Zig type-name strings ("f64", "i64", "u32",
// "u8", "*anyopaque", …); WriteScalar/ReadScalar already understand those
// spellings, so identical source type-checks and runs on both runtimes.
//
// Zig-specific methods (writeNative, readNative, readNativeAll) are stubbed.
func bufferModule() vm.Value {
	m := mod()
	bufInit := mod()
	bufInit.MapSet("init", fn("Buffer.init", bufferInit))
	m.MapSet("Buffer", bufInit)
	return m
}

// bufferState is the storage shared by a Buffer instance's methods: a growing
// slice for the string API and a lazily-pinned FFI block for the Zig/pointer API.
type bufferState struct {
	bytes []byte  // growing slice backing the string/byte API
	cap   int     // capacity passed to Buffer.init; size of the pinned block
	addr  uintptr // pinned FFI block base, or 0 if not yet allocated
	freed bool    // collect() has released the pinned block
}

func bufferInit(_ context.Context, args []vm.Value) (vm.Value, error) {
	cap := 0
	if len(args) >= 1 && args[0].IsInt() {
		cap = int(args[0].AsInt())
	}
	return makeBufferValue(&bufferState{bytes: make([]byte, 0, cap), cap: cap}), nil
}

// ensurePinned allocates the pinned FFI block backing the Zig/pointer API on
// first use, sized to the buffer's capacity. The block is zeroed, so reading
// before writing yields zeros.
func (st *bufferState) ensurePinned() (uintptr, error) {
	if st.freed {
		return 0, fmt.Errorf("Buffer: use after collect()")
	}
	if st.addr != 0 {
		return st.addr, nil
	}
	if st.cap <= 0 {
		return 0, fmt.Errorf("Buffer: the pointer/Zig API needs a capacity; use Buffer.init(capacity)")
	}
	addr, err := buzz.AllocFFI(st.cap)
	if err != nil {
		return 0, err
	}
	st.addr = addr
	return addr, nil
}

func makeBufferValue(st *bufferState) vm.Value {
	buf := &st.bytes
	m := mod()

	m.MapSet("write", fn("Buffer.write", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 || !args[0].IsList() {
			return vm.Null, fmt.Errorf("Buffer.write: requires a [int] bytes argument")
		}
		for _, item := range args[0].ListItems() {
			if !item.IsInt() {
				return vm.Null, fmt.Errorf("Buffer.write: list must contain int values")
			}
			*buf = append(*buf, byte(item.AsInt()))
		}
		return vm.Null, nil
	}))

	m.MapSet("writeString", fn("Buffer.writeString", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 || !args[0].IsStr() {
			return vm.Null, fmt.Errorf("Buffer.writeString: requires a str argument")
		}
		*buf = append(*buf, args[0].AsString()...)
		return vm.Null, nil
	}))

	m.MapSet("read", fn("Buffer.read", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 || !args[0].IsInt() {
			return vm.Null, fmt.Errorf("Buffer.read: requires an int n argument")
		}
		n := int(args[0].AsInt())
		if n < 0 {
			return vm.Null, fmt.Errorf("Buffer.read: n must be >= 0")
		}
		if n > len(*buf) {
			n = len(*buf)
		}
		chunk := (*buf)[:n]
		*buf = (*buf)[n:]
		items := make([]vm.Value, len(chunk))
		for i, b := range chunk {
			items[i] = vm.IntValue(int64(b))
		}
		return vm.ListValue(items), nil
	}))

	m.MapSet("readAll", fn("Buffer.readAll", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		items := make([]vm.Value, len(*buf))
		for i, b := range *buf {
			items[i] = vm.IntValue(int64(b))
		}
		*buf = (*buf)[:0]
		return vm.ListValue(items), nil
	}))

	// len(align) reports the buffer length divided by align. For a pinned
	// (capacity) buffer that is the capacity; for the string API it is the number
	// of bytes written. align defaults to 1, so len() is the byte count.
	m.MapSet("len", fn("Buffer.len", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		align := 1
		if len(args) >= 1 && args[0].IsInt() && args[0].AsInt() > 0 {
			align = int(args[0].AsInt())
		}
		n := len(*buf)
		if st.addr != 0 {
			n = st.cap
		}
		return vm.IntValue(int64(n / align)), nil
	}))

	m.MapSet("isEmpty", fn("Buffer.isEmpty", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return vm.BoolValue(len(*buf) == 0), nil
	}))

	m.MapSet("at", fn("Buffer.at", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 || !args[0].IsInt() {
			return vm.Null, fmt.Errorf("Buffer.at: requires an int index argument")
		}
		idx := int(args[0].AsInt())
		if idx < 0 || idx >= len(*buf) {
			return vm.Null, fmt.Errorf("Buffer.at: index %d out of range [0, %d)", idx, len(*buf))
		}
		return vm.IntValue(int64((*buf)[idx])), nil
	}))

	m.MapSet("setAt", fn("Buffer.setAt", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 2 || !args[0].IsInt() || !args[1].IsInt() {
			return vm.Null, fmt.Errorf("Buffer.setAt: requires (int index, int value)")
		}
		idx := int(args[0].AsInt())
		if idx < 0 || idx >= len(*buf) {
			return vm.Null, fmt.Errorf("Buffer.setAt: index %d out of range [0, %d)", idx, len(*buf))
		}
		(*buf)[idx] = byte(args[1].AsInt())
		return vm.Null, nil
	}))

	m.MapSet("toString", fn("Buffer.toString", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		return vm.StrValue(string(*buf)), nil
	}))

	m.MapSet("toList", fn("Buffer.toList", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		items := make([]vm.Value, len(*buf))
		for i, b := range *buf {
			items[i] = vm.IntValue(int64(b))
		}
		return vm.ListValue(items), nil
	}))

	m.MapSet("split", fn("Buffer.split", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 || !args[0].IsStr() {
			return vm.Null, fmt.Errorf("Buffer.split: requires a str separator argument")
		}
		sep := []byte(args[0].AsString())
		parts := bytes.Split(*buf, sep)
		result := make([]vm.Value, len(parts))
		for i, p := range parts {
			chunk := make([]byte, len(p))
			copy(chunk, p)
			result[i] = makeBufferValue(&bufferState{bytes: chunk, cap: len(chunk)})
		}
		return vm.ListValue(result), nil
	}))

	m.MapSet("trim", fn("Buffer.trim", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		*buf = bytes.TrimSpace(*buf)
		return vm.Null, nil
	}))

	m.MapSet("toFloat", fn("Buffer.toFloat", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		f, err := strconv.ParseFloat(string(*buf), 64)
		if err != nil {
			return vm.Null, fmt.Errorf("Buffer.toFloat: %w", err)
		}
		return vm.FloatValue(f), nil
	}))

	m.MapSet("toInteger", fn("Buffer.toInteger", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		n, err := strconv.ParseInt(string(*buf), 10, 64)
		if err != nil {
			return vm.Null, fmt.Errorf("Buffer.toInteger: %w", err)
		}
		return vm.IntValue(n), nil
	}))

	// collect frees the pinned FFI block if one was allocated (the Zig/pointer
	// API was used), and is idempotent — matching upstream Buffer.collect's
	// double-free guard. It is a no-op for a purely in-memory (string-API) buffer.
	m.MapSet("collect", fn("Buffer.collect", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		if st.addr != 0 && !st.freed {
			if err := buzz.FreeFFI(st.addr); err != nil {
				return vm.Null, err
			}
		}
		st.freed = true
		return vm.Null, nil
	}))

	// ptr(at, align) returns the machine address of the pinned block at byte
	// offset `at` — the value handed to a C out-parameter. align is an upstream
	// alignment hint; addresses here are byte-addressed, so it is accepted and
	// ignored. The block is allocated on first use.
	m.MapSet("ptr", fn("Buffer.ptr", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		at := 0
		if len(args) >= 1 && args[0].IsInt() {
			at = int(args[0].AsInt())
		}
		addr, err := st.ensurePinned()
		if err != nil {
			return vm.Null, err
		}
		if at < 0 || at > st.cap {
			return vm.Null, fmt.Errorf("Buffer.ptr: offset %d out of range [0, %d]", at, st.cap)
		}
		// The pinned block lives in the Go heap (well below 2^47), so an int is
		// lossless here; foreign pointers from C use the float64 `ud` path.
		return vm.IntValue(int64(addr) + int64(at)), nil
	}))

	// writeZAt(at, zigType, values) stores the list `values` as consecutive
	// zigType scalars starting at byte offset `at`. Mirrors upstream
	// Buffer.writeZAt::<T>; the discarded ::<T> type argument is parsed away.
	m.MapSet("writeZAt", fn("Buffer.writeZAt", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 3 || !args[0].IsInt() || !args[1].IsStr() || !args[2].IsList() {
			return vm.Null, fmt.Errorf("Buffer.writeZAt: requires (int at, str zigType, [any] values)")
		}
		at := int(args[0].AsInt())
		zigType := args[1].AsString()
		size, _, ok := buzz.CTypeLayout(zigType)
		if !ok {
			return vm.Null, fmt.Errorf("Buffer.writeZAt: unknown type %q", zigType)
		}
		addr, err := st.ensurePinned()
		if err != nil {
			return vm.Null, err
		}
		isFloat := zigType == "float" || zigType == "double" || zigType == "f32" || zigType == "f64"
		for idx, v := range args[2].ListItems() {
			var i int64
			var f float64
			switch {
			case isFloat && v.IsFloat():
				f = v.AsFloat()
			case isFloat && v.IsInt():
				f = float64(v.AsInt())
			case v.IsUD():
				// A foreign pointer (`ud`): its 64-bit address bits go through the
				// integer write path (lossless).
				i = int64(v.AsUD())
			case !isFloat && v.IsInt():
				i = v.AsInt()
			default:
				return vm.Null, fmt.Errorf("Buffer.writeZAt: value %d for %q must be %s", idx, zigType, numKind(isFloat))
			}
			if err := buzz.WriteScalar(addr, at+idx*size, zigType, i, f, isFloat); err != nil {
				return vm.Null, err
			}
		}
		return vm.Null, nil
	}))

	// readZAt(at, zigType) reads one zigType scalar at ELEMENT INDEX `at` — the
	// byte offset is `at * sizeof(zigType)`. This matches upstream buzz, which is
	// asymmetric: its read path multiplies by the type size while writeZAt's `at`
	// (above) is a raw byte offset. Replicated here so one .buzz source runs on
	// both runtimes (e.g. write a CGPoint's y at byte 8, read it back at index 1).
	m.MapSet("readZAt", fn("Buffer.readZAt", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 2 || !args[0].IsInt() || !args[1].IsStr() {
			return vm.Null, fmt.Errorf("Buffer.readZAt: requires (int at, str zigType)")
		}
		addr, err := st.ensurePinned()
		if err != nil {
			return vm.Null, err
		}
		zigType := args[1].AsString()
		size, _, ok := buzz.CTypeLayout(zigType)
		if !ok {
			return vm.Null, fmt.Errorf("Buffer.readZAt: unknown type %q", zigType)
		}
		i, f, isFloat, err := buzz.ReadScalar(addr, int(args[0].AsInt())*size, zigType)
		if err != nil {
			return vm.Null, err
		}
		if isFloat {
			return vm.FloatValue(f), nil
		}
		if buzz.IsPointerCType(zigType) {
			// A foreign pointer (`ud`): box the full 64-bit address losslessly.
			return vm.UDValue(uintptr(uint64(i))), nil
		}
		return vm.IntValue(i), nil
	}))

	// Zig ABI methods: stub with a clear error.
	for _, name := range []string{"writeNative", "readNative", "readNativeAll"} {
		name := name
		m.MapSet(name, fn("Buffer."+name, unsupported("Buffer."+name, "Zig ABI methods are not supported in the magus/buzz embedding")))
	}

	return m
}
