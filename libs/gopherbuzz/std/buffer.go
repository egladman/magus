package std

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"strings"

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
	// owned are C strings allocated for pointer fields written by writeStruct. The
	// buffer owns them because the image outlives the values it was built from.
	owned []uintptr
	freed bool // collect() has released the pinned block
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
	// A buffer written through the byte/Zig API and only THEN handed to C has no
	// declared capacity - upstream's ffi.buzz calls a bare Buffer.init(), fills it
	// with writeZ, and passes ptr() to a foreign function. Sizing the block to what
	// has actually been written makes that work, and keeps the explicit
	// Buffer.init(capacity) form (an out-parameter a callee fills) exactly as it was.
	size := st.cap
	if size <= 0 {
		size = len(st.bytes)
	}
	if size <= 0 {
		return 0, fmt.Errorf("Buffer: the pointer/Zig API needs a capacity or some written bytes; use Buffer.init(capacity)")
	}
	addr, err := buzz.AllocFFI(size)
	if err != nil {
		return 0, err
	}
	// The block is a SNAPSHOT of what has been written. Pinning copies the bytes in
	// once; a later writeZ appends to st.bytes and does not reach C. That is enough
	// for the fill-then-pass shape, and the alternative (repinning per write) would
	// invalidate a pointer a callee may still hold.
	if st.cap <= 0 && len(st.bytes) > 0 {
		if err := buzz.WriteFFIBytes(addr, st.bytes); err != nil {
			return 0, err
		}
		st.cap = size
	}
	st.addr = addr
	return addr, nil
}

func makeBufferValue(st *bufferState) vm.Value {
	buf := &st.bytes
	m := mod()

	m.MapSet("write", fn("Buffer.write", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		// Upstream's write takes a STR; gopherbuzz's took a [int]. Both are accepted
		// rather than one replaced: the string form is what upstream's behavior suite
		// uses, and the byte-list form is what this package's own callers already
		// pass. They are unambiguous at the value level, so nothing has to choose.
		if len(args) >= 1 && args[0].IsStr() {
			*buf = append(*buf, args[0].AsString()...)
			return vm.Null, nil
		}
		if len(args) < 1 || !args[0].IsList() {
			return vm.Null, fmt.Errorf("Buffer.write: requires a str or [int] bytes argument")
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
		// A STR, as upstream returns. readAll and toList still hand back byte lists
		// for callers that want the numeric view.
		return vm.StrValue(string(chunk)), nil
	}))

	// The binary API. Upstream's Buffer is how a Buzz program serialises values, and
	// each of these has a fixed on-the-wire width that its reader mirrors.
	//
	// An INT is six bytes, little-endian, not eight: upstream's Integer is an i48.
	// Writing eight would round-trip fine here and disagree with every other Buzz
	// program reading the same bytes, which is the kind of divergence a byte format
	// cannot afford.
	const intWidth = 6

	m.MapSet("writeInt", fn("Buffer.writeInt", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 || !args[0].IsInt() {
			return vm.Null, fmt.Errorf("Buffer.writeInt: requires an int argument")
		}
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], uint64(args[0].AsInt()))
		*buf = append(*buf, b[:intWidth]...)
		return vm.Null, nil
	}))

	m.MapSet("readInt", fn("Buffer.readInt", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		// NULL on a short buffer rather than an error: upstream's caller writes
		// `buffer.readInt() ?? -1`, so the miss is a value the program handles.
		if len(*buf) < intWidth {
			return vm.Null, nil
		}
		var b [8]byte
		copy(b[:intWidth], (*buf)[:intWidth])
		u := binary.LittleEndian.Uint64(b[:])
		// Sign-extend from 48 bits so a negative round-trips.
		if u&(1<<47) != 0 {
			u |= uint64(0xFFFF) << 48
		}
		*buf = (*buf)[intWidth:]
		return vm.IntValue(int64(u)), nil
	}))

	m.MapSet("writeDouble", fn("Buffer.writeDouble", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 || !(args[0].IsFloat() || args[0].IsInt()) {
			return vm.Null, fmt.Errorf("Buffer.writeDouble: requires a double argument")
		}
		d := args[0].AsFloat()
		var b [8]byte
		binary.LittleEndian.PutUint64(b[:], math.Float64bits(d))
		*buf = append(*buf, b[:]...)
		return vm.Null, nil
	}))

	m.MapSet("readDouble", fn("Buffer.readDouble", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		if len(*buf) < 8 {
			return vm.Null, nil
		}
		u := binary.LittleEndian.Uint64((*buf)[:8])
		*buf = (*buf)[8:]
		return vm.FloatValue(math.Float64frombits(u)), nil
	}))

	m.MapSet("writeBoolean", fn("Buffer.writeBoolean", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 || !args[0].IsBool() {
			return vm.Null, fmt.Errorf("Buffer.writeBoolean: requires a bool argument")
		}
		var v byte
		if args[0].Bool() {
			v = 1
		}
		*buf = append(*buf, v)
		return vm.Null, nil
	}))

	m.MapSet("readBoolean", fn("Buffer.readBoolean", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		if len(*buf) < 1 {
			return vm.Null, nil
		}
		v := (*buf)[0]
		*buf = (*buf)[1:]
		return vm.BoolValue(v != 0), nil
	}))

	// empty() CLEARS the buffer. Note it is not the negation of isEmpty(), which
	// only reports - upstream names them that way and both are kept as upstream has
	// them rather than renamed for symmetry.
	m.MapSet("empty", fn("Buffer.empty", func(_ context.Context, _ []vm.Value) (vm.Value, error) {
		*buf = (*buf)[:0]
		return vm.Null, nil
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
		for _, a := range st.owned {
			_ = buzz.FreeFFI(a)
		}
		st.owned = nil
		st.freed = true
		return vm.Null, nil
	}))

	// ptr(at, align) returns the machine address of the pinned block at byte
	// offset `at` — the value handed to a C out-parameter. align is an upstream
	// alignment hint; addresses here are byte-addressed, so it is accepted and
	// ignored. The block is allocated on first use.
	// writeZ / readZAt are the Zig-typed binary API upstream's ffi.buzz uses to hand
	// a buffer to a foreign function: writeZ appends each value encoded at the C
	// width of zigType, readZAt decodes the element at an index.
	m.MapSet("writeZ", fn("Buffer.writeZ", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 2 || !args[0].IsStr() || !args[1].IsList() {
			return vm.Null, fmt.Errorf("Buffer.writeZ: requires (str zigType, [any] values)")
		}
		zt := args[0].AsString()
		size, err := intZigWidth(zt)
		if err != nil {
			return vm.Null, err
		}
		for _, v := range args[1].ListItems() {
			if !v.IsInt() {
				return vm.Null, errFFITypeMismatch(zt, "a non-int value cannot be written as an integer type")
			}
			st.bytes = appendLE(st.bytes, uint64(v.AsInt()), size)
		}
		return vm.Null, nil
	}))
	m.MapSet("readZAt", fn("Buffer.readZAt", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 2 || !args[0].IsInt() || !args[1].IsStr() {
			return vm.Null, fmt.Errorf("Buffer.readZAt: requires (int at, str zigType)")
		}
		zt := args[1].AsString()
		size, err := intZigWidth(zt)
		if err != nil {
			return vm.Null, err
		}
		at := int(args[0].AsInt())
		off := at * size
		if at < 0 || off+size > len(st.bytes) {
			return vm.Null, fmt.Errorf("Buffer.readZAt: index %d out of range", at)
		}
		return vm.IntValue(decodeLE(st.bytes[off:off+size], zt)), nil
	}))

	// writeStruct / readStruct move whole foreign structs through the buffer, laid
	// out exactly as C would - which is what lets a script stage an array of them
	// and hand the pointer to a callee.
	m.MapSet("writeStruct", fn("Buffer.writeStruct", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 2 || !args[1].IsList() {
			return vm.Null, fmt.Errorf("Buffer.writeStruct: requires (Type, [Type] values)")
		}
		types, offsets, size, err := foreignLayoutOf(args[0])
		if err != nil {
			return vm.Null, err
		}
		for _, inst := range args[1].ListItems() {
			block := make([]byte, size)
			for i, ct := range types {
				fv, ok := inst.ObjectFieldAt(i)
				if !ok {
					break
				}
				encodeField(st, block[offsets[i]:], ct, fv)
			}
			st.bytes = append(st.bytes, block...)
		}
		return vm.Null, nil
	}))
	m.MapSet("readStruct", fn("Buffer.readStruct", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 {
			return vm.Null, fmt.Errorf("Buffer.readStruct: requires the struct type")
		}
		types, offsets, size, err := foreignLayoutOf(args[0])
		if err != nil {
			return vm.Null, err
		}
		if len(st.bytes) < size {
			return vm.Null, fmt.Errorf("Buffer.readStruct: buffer holds %d bytes, the struct needs %d", len(st.bytes), size)
		}
		fields := make([]vm.Value, len(types))
		for i, ct := range types {
			fields[i] = decodeField(st.bytes[offsets[i]:], ct)
		}
		return args[0].NewInstance(fields)
	}))

	m.MapSet("ptr", fn("Buffer.ptr", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		at := 0
		if len(args) >= 1 && args[0].IsInt() {
			at = int(args[0].AsInt())
		}
		// `align` makes the offset an ELEMENT index rather than a byte count, which is
		// how upstream walks a typed buffer: ptr(1, align: alignOf("i32")) is the
		// second i32, not the second byte.
		if len(args) >= 2 && args[1].IsInt() && args[1].AsInt() > 0 {
			at *= int(args[1].AsInt())
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

// ffiTypeMismatch is a host error that presents as `ffi\FFITypeMismatchError`, so
// upstream's `catch (_: ffi\FFITypeMismatchError)` selects it. See vm.TypedError.
type ffiTypeMismatch struct {
	zigType string
	why     string
}

func (e ffiTypeMismatch) Error() string {
	return fmt.Sprintf("ffi: type mismatch for %q: %s", e.zigType, e.why)
}
func (e ffiTypeMismatch) BuzzError() map[string]string {
	return map[string]string{"zigType": e.zigType, "reason": e.why}
}
func (e ffiTypeMismatch) BuzzErrorType() string { return "FFITypeMismatchError" }

func errFFITypeMismatch(zigType, why string) error {
	return ffiTypeMismatch{zigType: zigType, why: why}
}

// intZigWidth returns the byte width of an INTEGER zig type, rejecting anything a
// Buzz int cannot faithfully carry.
//
// u64 is the interesting rejection, and upstream's ffi.buzz asserts it: a Buzz int
// is an i64, so half of u64's range has no representation and a silent wrap would
// be the worst possible answer. That is a property of the type pair, not of the
// call, which is why no `::<T>` argument is needed to detect it.
func intZigWidth(zigType string) (int, error) {
	switch zigType {
	case "i8", "u8":
		return 1, nil
	case "i16", "u16":
		return 2, nil
	case "i32", "u32", "c_int", "c_uint":
		return 4, nil
	case "i64", "isize", "c_long", "c_longlong":
		return 8, nil
	case "u64", "usize", "c_ulong", "c_ulonglong":
		return 0, errFFITypeMismatch(zigType, "a buzz int is an i64 and cannot represent the whole unsigned 64-bit range")
	}
	return 0, errFFITypeMismatch(zigType, "not an integer type buzz can write")
}

// appendLE appends the low `size` bytes of v, little-endian.
func appendLE(dst []byte, v uint64, size int) []byte {
	for i := 0; i < size; i++ {
		dst = append(dst, byte(v>>(8*i)))
	}
	return dst
}

// decodeLE reads a little-endian integer, sign-extending a signed zig type so a
// negative value round-trips through writeZ/readZAt.
func decodeLE(b []byte, zigType string) int64 {
	var u uint64
	for i := len(b) - 1; i >= 0; i-- {
		u = u<<8 | uint64(b[i])
	}
	if strings.HasPrefix(zigType, "i") || strings.HasPrefix(zigType, "c_long") || zigType == "c_int" || zigType == "isize" {
		shift := uint(64 - 8*len(b))
		return int64(u<<shift) >> shift
	}
	return int64(u)
}

// foreignLayoutOf resolves a zdef struct TYPE value to its field C types, byte
// offsets and total size.
func foreignLayoutOf(v vm.Value) (types []string, offsets []int, size int, err error) {
	name := v.ObjectTypeName()
	if name == "" {
		return nil, nil, 0, fmt.Errorf("expected a struct type, got %s", v.Kind())
	}
	types, ok := buzz.ForeignStructTypes(name)
	if !ok {
		return nil, nil, 0, fmt.Errorf("%s is not a foreign struct declared by zdef", name)
	}
	size, _, offsets, err = buzz.StructLayout(types)
	return types, offsets, size, err
}

// encodeField writes one field into a struct block, little-endian.
//
// A POINTER field is stored as a real address: upstream writes a struct into a
// buffer and reads it back expecting the string to survive, which only works if the
// bytes carry a pointer to it. The C string is allocated here and owned by the
// BUFFER - freed by collect() - because the image may outlive the value it came
// from. An image written by one process and read by another would of course hold a
// dangling pointer; that is inherent to putting a C struct in a byte buffer.
func encodeField(st *bufferState, dst []byte, ctype string, v vm.Value) {
	size, _, ok := buzz.CTypeLayout(ctype)
	if !ok || size > len(dst) {
		return
	}
	switch {
	case strings.Contains(ctype, "*"):
		if !v.IsStr() {
			return
		}
		addr, err := buzz.AllocCString(v.AsString())
		if err != nil {
			return
		}
		st.owned = append(st.owned, addr)
		binary.LittleEndian.PutUint64(dst[:8], uint64(addr))
	case ctype == "f64" || ctype == "double":
		binary.LittleEndian.PutUint64(dst[:8], math.Float64bits(v.AsFloat()))
	case ctype == "f32" || ctype == "float":
		binary.LittleEndian.PutUint32(dst[:4], math.Float32bits(float32(v.AsFloat())))
	default:
		u := uint64(v.AsInt())
		for i := 0; i < size; i++ {
			dst[i] = byte(u >> (8 * i))
		}
	}
}

// decodeField reads one field back out of a struct block.
func decodeField(src []byte, ctype string) vm.Value {
	size, _, ok := buzz.CTypeLayout(ctype)
	if !ok || size > len(src) {
		return vm.Null
	}
	switch {
	case strings.Contains(ctype, "*"):
		return vm.StrValue(buzz.ReadCString(uintptr(binary.LittleEndian.Uint64(src[:8]))))
	case ctype == "f64" || ctype == "double":
		return vm.FloatValue(math.Float64frombits(binary.LittleEndian.Uint64(src[:8])))
	case ctype == "f32" || ctype == "float":
		return vm.FloatValue(float64(math.Float32frombits(binary.LittleEndian.Uint32(src[:4]))))
	}
	return vm.IntValue(decodeLE(src[:size], ctype))
}
