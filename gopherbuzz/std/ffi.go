package std

import (
	"context"
	"fmt"

	buzz "github.com/egladman/gopherbuzz"
)

// ffiModule builds the "ffi" module.
//
// Upstream Buzz's ffi module is Zig-ABI native: sizeOf/alignOf take Zig types
// and are answered by an embedded Zig compiler's comptime reflection. This
// embedding has no Zig, so the same *capabilities* are offered C-ABI-natively —
// every type argument is a C type-name string from the zdef() subset
// ("int", "double", "char*", "int64_t", …) instead of a Zig type:
//
//	ffi.sizeOf("double")        // 8
//	ffi.alignOf("int")          // 4
//	ffi.sizeOfStruct(["int", "char*"])  // 16 on LP64 (4 + 4 pad + 8)
//
// Memory exchange with C (out-parameters, by-reference structs) goes through a
// pinned-allocation API. The script allocates, hands the address to a zdef
// pointer parameter, the C function fills it, and the script reads the result:
//
//	const buf = ffi.alloc(8);
//	lib.gettimeofday(buf, 0);          // void* out-param
//	const secs = ffi.read(buf, 0, "long");
//	ffi.free(buf);
//
// read/write operate only on memory ffi.alloc returned (bounds-checked); see
// vm/ffi_mem.go for why dereferencing a foreign address is deliberately excluded.
func ffiModule() buzz.Value {
	m := mod()

	// cstr mirrors upstream's ffi.cstr. In this embedding a Buzz str is already a
	// valid null-terminated char* at the zdef boundary, so cstr is the identity —
	// kept so scripts written against upstream Buzz still parse and run.
	m.MapSet("cstr", fn("ffi.cstr", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsStr() {
			return buzz.Null, fmt.Errorf("ffi.cstr: requires a str argument")
		}
		return args[0], nil
	}))

	// callback(fn, retType, paramTypes): wrap a Buzz function as a C function
	// pointer (returned as an int address) to pass where C expects a callback —
	// e.g. the comparator of qsort. The matching zdef parameter is declared void*.
	m.MapSet("callback", fn("ffi.callback", func(ctx context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 3 || !args[0].IsFun() || !args[1].IsStr() || !args[2].IsList() {
			return buzz.Null, fmt.Errorf("ffi.callback: requires (fun fn, str retType, [str] paramTypes)")
		}
		params, err := stringList("ffi.callback paramTypes", args[2:])
		if err != nil {
			return buzz.Null, err
		}
		addr, err := buzz.MakeCallback(ctx, args[0], args[1].AsString(), params)
		if err != nil {
			return buzz.Null, err
		}
		return buzz.IntValue(int64(addr)), nil
	}))

	m.MapSet("sizeOf", fn("ffi.sizeOf", typeMetric("ffi.sizeOf", func(size, _ int) int { return size })))
	m.MapSet("alignOf", fn("ffi.alignOf", typeMetric("ffi.alignOf", func(_, align int) int { return align })))

	m.MapSet("sizeOfStruct", fn("ffi.sizeOfStruct", structMetric("ffi.sizeOfStruct", func(size, _ int) int { return size })))
	m.MapSet("alignOfStruct", fn("ffi.alignOfStruct", structMetric("ffi.alignOfStruct", func(_, align int) int { return align })))

	// structLayout returns {size, align, offsets} so a script can place each field
	// at its correct C offset inside an alloc block.
	m.MapSet("structLayout", fn("ffi.structLayout", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		fields, err := stringList("ffi.structLayout", args)
		if err != nil {
			return buzz.Null, err
		}
		size, align, offsets, err := buzz.StructLayout(fields)
		if err != nil {
			return buzz.Null, err
		}
		offVals := make([]buzz.Value, len(offsets))
		for i, o := range offsets {
			offVals[i] = buzz.IntValue(int64(o))
		}
		out := buzz.NewMap()
		out.MapSet("size", buzz.IntValue(int64(size)))
		out.MapSet("align", buzz.IntValue(int64(align)))
		out.MapSet("offsets", buzz.ListValue(offVals))
		return out, nil
	}))

	m.MapSet("alloc", fn("ffi.alloc", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsInt() {
			return buzz.Null, fmt.Errorf("ffi.alloc: requires an int size argument")
		}
		addr, err := buzz.AllocFFI(int(args[0].AsInt()))
		if err != nil {
			return buzz.Null, err
		}
		return buzz.IntValue(int64(addr)), nil
	}))

	m.MapSet("free", fn("ffi.free", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsInt() {
			return buzz.Null, fmt.Errorf("ffi.free: requires an int address argument")
		}
		if err := buzz.FreeFFI(uintptr(args[0].AsInt())); err != nil {
			return buzz.Null, err
		}
		return buzz.Null, nil
	}))

	// write(addr, offset, ctype, value): store a scalar. float/double read value
	// as a float; every other type reads it as an int.
	m.MapSet("write", fn("ffi.write", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 4 || !args[0].IsInt() || !args[1].IsInt() || !args[2].IsStr() {
			return buzz.Null, fmt.Errorf("ffi.write: requires (int addr, int offset, str ctype, value)")
		}
		addr := uintptr(args[0].AsInt())
		offset := int(args[1].AsInt())
		ctype := args[2].AsString()
		val := args[3]
		isFloat := ctype == "float" || ctype == "double"
		var i int64
		var f float64
		switch {
		case isFloat && val.IsFloat():
			f = val.AsFloat()
		case isFloat && val.IsInt():
			f = float64(val.AsInt())
		case val.IsInt():
			i = val.AsInt()
		default:
			return buzz.Null, fmt.Errorf("ffi.write: value for %q must be %s", ctype, numKind(isFloat))
		}
		if err := buzz.WriteScalar(addr, offset, ctype, i, f, isFloat); err != nil {
			return buzz.Null, err
		}
		return buzz.Null, nil
	}))

	// read(addr, offset, ctype): load a scalar, returning float for float/double
	// and int otherwise.
	m.MapSet("read", fn("ffi.read", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 3 || !args[1].IsInt() || !args[2].IsStr() {
			return buzz.Null, fmt.Errorf("ffi.read: requires (addr, int offset, str ctype)")
		}
		var addr uintptr
		switch {
		case args[0].IsUD():
			addr = args[0].AsUD()
		case args[0].IsInt():
			addr = uintptr(args[0].AsInt())
		default:
			return buzz.Null, fmt.Errorf("ffi.read: addr must be an int or ud")
		}
		ctype := args[2].AsString()
		i, f, isFloat, err := buzz.ReadScalar(addr, int(args[1].AsInt()), ctype)
		if err != nil {
			return buzz.Null, err
		}
		if isFloat {
			return buzz.FloatValue(f), nil
		}
		if buzz.IsPointerCType(ctype) {
			return buzz.UDValue(uintptr(uint64(i))), nil
		}
		return buzz.IntValue(i), nil
	}))

	return m
}

func numKind(isFloat bool) string {
	if isFloat {
		return "a float"
	}
	return "an int"
}

// typeMetric adapts ffi.sizeOf / ffi.alignOf: each takes one C type-name string
// and returns the chosen layout metric.
func typeMetric(name string, pick func(size, align int) int) func(context.Context, []buzz.Value) (buzz.Value, error) {
	return func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsStr() {
			return buzz.Null, fmt.Errorf("%s: requires a C type name str (e.g. \"int\", \"double\", \"char*\"); FFI here is C-ABI, not Zig", name)
		}
		size, align, ok := buzz.CTypeLayout(args[0].AsString())
		if !ok {
			return buzz.Null, fmt.Errorf("%s: unknown C type %q", name, args[0].AsString())
		}
		return buzz.IntValue(int64(pick(size, align))), nil
	}
}

// structMetric adapts ffi.sizeOfStruct / ffi.alignOfStruct: each takes a list of
// C field type-name strings.
func structMetric(name string, pick func(size, align int) int) func(context.Context, []buzz.Value) (buzz.Value, error) {
	return func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		fields, err := stringList(name, args)
		if err != nil {
			return buzz.Null, err
		}
		size, align, _, err := buzz.StructLayout(fields)
		if err != nil {
			return buzz.Null, err
		}
		return buzz.IntValue(int64(pick(size, align))), nil
	}
}

// stringList reads the first argument as a list of C type-name strings.
func stringList(name string, args []buzz.Value) ([]string, error) {
	if len(args) < 1 || !args[0].IsList() {
		return nil, fmt.Errorf("%s: requires a [str] list of C field type names", name)
	}
	items := args[0].ListItems()
	out := make([]string, len(items))
	for i, it := range items {
		if !it.IsStr() {
			return nil, fmt.Errorf("%s: field %d must be a C type name str", name, i)
		}
		out[i] = it.AsString()
	}
	return out, nil
}
