package std

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	buzz "github.com/egladman/gopherbuzz"
)

// bufferModule builds the "buffer" module matching Buzz's buffer reference:
// https://buzz-lang.dev/0.5.0/reference/std/buffer.html
//
// Buffer is a mutable byte container.  Each instance is modelled as a
// buzz map whose methods close over a shared *[]byte — the same pattern
// used for File in io.go.
//
// Zig-specific methods (writeNative, readNative, readNativeAll) are stubbed.
func bufferModule() buzz.Value {
	m := mod()
	bufInit := mod()
	bufInit.MapSet("init", fn("Buffer.init", bufferInit))
	m.MapSet("Buffer", bufInit)
	return m
}

func bufferInit(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	cap := 0
	if len(args) >= 1 && args[0].IsInt() {
		cap = int(args[0].AsInt())
	}
	buf := make([]byte, 0, cap)
	return makeBufferValue(&buf), nil
}

func makeBufferValue(buf *[]byte) buzz.Value {
	m := mod()

	m.MapSet("write", fn("Buffer.write", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsList() {
			return buzz.Null, fmt.Errorf("Buffer.write: requires a [int] bytes argument")
		}
		for _, item := range args[0].ListItems() {
			if !item.IsInt() {
				return buzz.Null, fmt.Errorf("Buffer.write: list must contain int values")
			}
			*buf = append(*buf, byte(item.AsInt()))
		}
		return buzz.Null, nil
	}))

	m.MapSet("writeString", fn("Buffer.writeString", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsStr() {
			return buzz.Null, fmt.Errorf("Buffer.writeString: requires a str argument")
		}
		*buf = append(*buf, args[0].AsString()...)
		return buzz.Null, nil
	}))

	m.MapSet("read", fn("Buffer.read", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsInt() {
			return buzz.Null, fmt.Errorf("Buffer.read: requires an int n argument")
		}
		n := int(args[0].AsInt())
		if n < 0 {
			return buzz.Null, fmt.Errorf("Buffer.read: n must be >= 0")
		}
		if n > len(*buf) {
			n = len(*buf)
		}
		chunk := (*buf)[:n]
		*buf = (*buf)[n:]
		items := make([]buzz.Value, len(chunk))
		for i, b := range chunk {
			items[i] = buzz.IntValue(int64(b))
		}
		return buzz.ListValue(items), nil
	}))

	m.MapSet("readAll", fn("Buffer.readAll", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		items := make([]buzz.Value, len(*buf))
		for i, b := range *buf {
			items[i] = buzz.IntValue(int64(b))
		}
		*buf = (*buf)[:0]
		return buzz.ListValue(items), nil
	}))

	m.MapSet("len", fn("Buffer.len", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return buzz.IntValue(int64(len(*buf))), nil
	}))

	m.MapSet("isEmpty", fn("Buffer.isEmpty", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return buzz.BoolValue(len(*buf) == 0), nil
	}))

	m.MapSet("at", fn("Buffer.at", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsInt() {
			return buzz.Null, fmt.Errorf("Buffer.at: requires an int index argument")
		}
		idx := int(args[0].AsInt())
		if idx < 0 || idx >= len(*buf) {
			return buzz.Null, fmt.Errorf("Buffer.at: index %d out of range [0, %d)", idx, len(*buf))
		}
		return buzz.IntValue(int64((*buf)[idx])), nil
	}))

	m.MapSet("setAt", fn("Buffer.setAt", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 2 || !args[0].IsInt() || !args[1].IsInt() {
			return buzz.Null, fmt.Errorf("Buffer.setAt: requires (int index, int value)")
		}
		idx := int(args[0].AsInt())
		if idx < 0 || idx >= len(*buf) {
			return buzz.Null, fmt.Errorf("Buffer.setAt: index %d out of range [0, %d)", idx, len(*buf))
		}
		(*buf)[idx] = byte(args[1].AsInt())
		return buzz.Null, nil
	}))

	m.MapSet("toString", fn("Buffer.toString", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return buzz.StrValue(string(*buf)), nil
	}))

	m.MapSet("toList", fn("Buffer.toList", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		items := make([]buzz.Value, len(*buf))
		for i, b := range *buf {
			items[i] = buzz.IntValue(int64(b))
		}
		return buzz.ListValue(items), nil
	}))

	m.MapSet("split", fn("Buffer.split", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 || !args[0].IsStr() {
			return buzz.Null, fmt.Errorf("Buffer.split: requires a str separator argument")
		}
		sep := []byte(args[0].AsString())
		parts := bytes.Split(*buf, sep)
		result := make([]buzz.Value, len(parts))
		for i, p := range parts {
			chunk := make([]byte, len(p))
			copy(chunk, p)
			result[i] = makeBufferValue(&chunk)
		}
		return buzz.ListValue(result), nil
	}))

	m.MapSet("trim", fn("Buffer.trim", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		*buf = bytes.TrimSpace(*buf)
		return buzz.Null, nil
	}))

	m.MapSet("toFloat", fn("Buffer.toFloat", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		f, err := strconv.ParseFloat(string(*buf), 64)
		if err != nil {
			return buzz.Null, fmt.Errorf("Buffer.toFloat: %w", err)
		}
		return buzz.FloatValue(f), nil
	}))

	m.MapSet("toInteger", fn("Buffer.toInteger", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		n, err := strconv.ParseInt(string(*buf), 10, 64)
		if err != nil {
			return buzz.Null, fmt.Errorf("Buffer.toInteger: %w", err)
		}
		return buzz.IntValue(n), nil
	}))

	// collect is Buzz's IDisposable method — no-op for an in-memory buffer.
	m.MapSet("collect", fn("Buffer.collect", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return buzz.Null, nil
	}))

	// Zig ABI methods: stub with a clear error.
	for _, name := range []string{"writeNative", "readNative", "readNativeAll"} {
		name := name
		m.MapSet(name, fn("Buffer."+name, unsupported("Buffer."+name, "Zig ABI methods are not supported in the magus/buzz embedding")))
	}

	return m
}
