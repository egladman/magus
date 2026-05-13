package std

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"strconv"
	"unicode/utf8"

	buzz "github.com/egladman/gopherbuzz"
)

// coreModule builds the "std" module matching Buzz's std reference:
// https://buzz-lang.dev/0.5.0/reference/std/std.html
//
// out receives std.print output; Register passes os.Stdout. Embeddings that
// capture a program's output (e.g. a browser playground) supply their own
// writer via RegisterWithOutput.
func coreModule(out io.Writer) buzz.Value {
	m := mod()
	m.MapSet("assert", fn("std.assert", stdAssert))
	m.MapSet("print", fn("std.print", makeStdPrint(out)))
	m.MapSet("parseInt", fn("std.parseInt", stdParseInt))
	m.MapSet("parseDouble", fn("std.parseDouble", stdParseDouble))
	m.MapSet("toInt", fn("std.toInt", stdToInt))
	m.MapSet("toDouble", fn("std.toDouble", stdToDouble))
	m.MapSet("char", fn("std.char", stdChar))
	m.MapSet("random", fn("std.random", stdRandom))
	m.MapSet("currentFiber", fn("std.currentFiber", stdCurrentFiber))
	m.MapSet("panic", fn("std.panic", stdPanic))
	// toUd / parseUd require Zig userdata — not supported in the Go embedding.
	m.MapSet("toUd", fn("std.toUd", unsupported("std.toUd", "userdata (ud) is a Zig-specific type")))
	m.MapSet("parseUd", fn("std.parseUd", unsupported("std.parseUd", "userdata (ud) is a Zig-specific type")))
	return m
}

// unsupported returns a Callable that always fails with a descriptive error.
func unsupported(name, reason string) func(context.Context, []buzz.Value) (buzz.Value, error) {
	return func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return buzz.Null, fmt.Errorf("%s: not supported in the magus/buzz embedding: %s", name, reason)
	}
}

func stdAssert(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 {
		return buzz.Null, fmt.Errorf("std.assert: requires at least 1 argument")
	}
	if !args[0].Bool() {
		msg := "assertion failed"
		if len(args) >= 2 && args[1].IsStr() {
			msg = args[1].AsString()
		}
		return buzz.Null, fmt.Errorf("std.assert: %s", msg)
	}
	return buzz.Null, nil
}

func makeStdPrint(out io.Writer) func(context.Context, []buzz.Value) (buzz.Value, error) {
	return func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		if len(args) < 1 {
			fmt.Fprintln(out)
			return buzz.Null, nil
		}
		fmt.Fprintln(out, args[0].String())
		return buzz.Null, nil
	}
}

func stdParseInt(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return buzz.Null, fmt.Errorf("std.parseInt: requires a str argument")
	}
	n, err := strconv.ParseInt(args[0].AsString(), 10, 64)
	if err != nil {
		return buzz.Null, nil // return null on parse failure (Buzz returns int?)
	}
	return buzz.IntValue(n), nil
}

func stdParseDouble(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return buzz.Null, fmt.Errorf("std.parseDouble: requires a str argument")
	}
	f, err := strconv.ParseFloat(args[0].AsString(), 64)
	if err != nil {
		return buzz.Null, nil // return null on parse failure (Buzz returns double?)
	}
	return buzz.FloatValue(f), nil
}

func stdToInt(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 {
		return buzz.Null, fmt.Errorf("std.toInt: requires 1 argument")
	}
	switch {
	case args[0].IsFloat():
		return buzz.IntValue(int64(args[0].AsFloat())), nil
	case args[0].IsInt():
		return args[0], nil
	default:
		return buzz.Null, fmt.Errorf("std.toInt: expected double, got %s", args[0].Kind())
	}
}

func stdToDouble(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 {
		return buzz.Null, fmt.Errorf("std.toDouble: requires 1 argument")
	}
	switch {
	case args[0].IsInt():
		return buzz.FloatValue(float64(args[0].AsInt())), nil
	case args[0].IsFloat():
		return args[0], nil
	default:
		return buzz.Null, fmt.Errorf("std.toDouble: expected int, got %s", args[0].Kind())
	}
}

func stdChar(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	if len(args) < 1 || !args[0].IsInt() {
		return buzz.Null, fmt.Errorf("std.char: requires an int argument")
	}
	r := rune(args[0].AsInt())
	if !utf8.ValidRune(r) {
		return buzz.Null, fmt.Errorf("std.char: invalid Unicode code point %d", args[0].AsInt())
	}
	return buzz.StrValue(string(r)), nil
}

func stdRandom(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	var lo, hi int64
	switch len(args) {
	case 0:
		// random() with no args: 0..maxInt
		return buzz.IntValue(rand.Int64()), nil //nolint:gosec
	case 1:
		if !args[0].IsInt() {
			return buzz.Null, fmt.Errorf("std.random: arguments must be int")
		}
		hi = args[0].AsInt()
		lo = 0
	case 2:
		if !args[0].IsInt() || !args[1].IsInt() {
			return buzz.Null, fmt.Errorf("std.random: arguments must be int")
		}
		lo = args[0].AsInt()
		hi = args[1].AsInt()
	default:
		return buzz.Null, fmt.Errorf("std.random: expected 0, 1 or 2 arguments")
	}
	if lo > hi {
		return buzz.Null, fmt.Errorf("std.random: min (%d) > max (%d)", lo, hi)
	}
	if lo == hi {
		return buzz.IntValue(lo), nil
	}
	n := rand.Int64N(hi-lo) + lo //nolint:gosec
	return buzz.IntValue(n), nil
}

// stdCurrentFiber returns null in the Go embedding — there is no current fiber
// object from the host's perspective. A running Buzz fiber has no way to surface
// its own value to Go; this stub keeps scripts that call currentFiber() from
// crashing.
func stdCurrentFiber(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
	return buzz.Null, nil
}

func stdPanic(_ context.Context, args []buzz.Value) (buzz.Value, error) {
	msg := "panic"
	if len(args) >= 1 && args[0].IsStr() {
		msg = args[0].AsString()
	}
	return buzz.Null, fmt.Errorf("std.panic: %s", msg)
}
