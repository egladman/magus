package std

import (
	"context"
	"fmt"
	"math"

	"github.com/egladman/gopherbuzz/vm"
)

// mathModule builds the "math" module matching Buzz's math reference:
// https://buzz-lang.dev/0.5.0/reference/std/math.html
func mathModule() vm.Value {
	m := mod()
	m.MapSet("pi", vm.FloatValue(math.Pi))
	m.MapSet("abs", fn("math.abs", mathAbs))
	m.MapSet("acos", fn("math.acos", mathUnary("math.acos", math.Acos)))
	m.MapSet("asin", fn("math.asin", mathUnary("math.asin", math.Asin)))
	m.MapSet("atan", fn("math.atan", mathUnary("math.atan", math.Atan)))
	m.MapSet("ceil", fn("math.ceil", mathCeil))
	m.MapSet("cos", fn("math.cos", mathUnary("math.cos", math.Cos)))
	m.MapSet("deg", fn("math.deg", mathUnary("math.deg", func(r float64) float64 { return r * 180 / math.Pi })))
	m.MapSet("exp", fn("math.exp", mathUnary("math.exp", math.Exp)))
	m.MapSet("floor", fn("math.floor", mathFloor))
	m.MapSet("log", fn("math.log", mathLog))
	m.MapSet("maxDouble", fn("math.maxDouble", mathBinaryFloat("math.maxDouble", func(a, b float64) float64 {
		if a > b {
			return a
		}
		return b
	})))
	m.MapSet("minDouble", fn("math.minDouble", mathBinaryFloat("math.minDouble", func(a, b float64) float64 {
		if a < b {
			return a
		}
		return b
	})))
	m.MapSet("maxInt", fn("math.maxInt", mathBinaryInt("math.maxInt", func(a, b int64) int64 {
		if a > b {
			return a
		}
		return b
	})))
	m.MapSet("minInt", fn("math.minInt", mathBinaryInt("math.minInt", func(a, b int64) int64 {
		if a < b {
			return a
		}
		return b
	})))
	m.MapSet("rad", fn("math.rad", mathUnary("math.rad", func(d float64) float64 { return d * math.Pi / 180 })))
	m.MapSet("sin", fn("math.sin", mathUnary("math.sin", math.Sin)))
	m.MapSet("sqrt", fn("math.sqrt", mathUnary("math.sqrt", math.Sqrt)))
	m.MapSet("tan", fn("math.tan", mathUnary("math.tan", math.Tan)))
	m.MapSet("pow", fn("math.pow", mathPow))
	return m
}

func toFloat(v vm.Value, name string) (float64, error) {
	switch {
	case v.IsFloat():
		return v.AsFloat(), nil
	case v.IsInt():
		return float64(v.AsInt()), nil
	default:
		return 0, fmt.Errorf("%s: expected double, got %s", name, v.Kind())
	}
}

func mathUnary(name string, f func(float64) float64) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 {
			return vm.Null, fmt.Errorf("%s: requires 1 argument", name)
		}
		x, err := toFloat(args[0], name)
		if err != nil {
			return vm.Null, err
		}
		return vm.FloatValue(f(x)), nil
	}
}

func mathBinaryFloat(name string, f func(float64, float64) float64) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 2 {
			return vm.Null, fmt.Errorf("%s: requires 2 arguments", name)
		}
		a, err := toFloat(args[0], name)
		if err != nil {
			return vm.Null, err
		}
		b, err := toFloat(args[1], name)
		if err != nil {
			return vm.Null, err
		}
		return vm.FloatValue(f(a, b)), nil
	}
}

func mathBinaryInt(name string, f func(int64, int64) int64) func(context.Context, []vm.Value) (vm.Value, error) {
	return func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 2 {
			return vm.Null, fmt.Errorf("%s: requires 2 arguments", name)
		}
		if !args[0].IsInt() || !args[1].IsInt() {
			return vm.Null, fmt.Errorf("%s: requires int arguments", name)
		}
		return vm.IntValue(f(args[0].AsInt(), args[1].AsInt())), nil
	}
}

func mathAbs(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 {
		return vm.Null, fmt.Errorf("math.abs: requires 1 argument")
	}
	x, err := toFloat(args[0], "math.abs")
	if err != nil {
		return vm.Null, err
	}
	return vm.FloatValue(math.Abs(x)), nil
}

func mathCeil(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 {
		return vm.Null, fmt.Errorf("math.ceil: requires 1 argument")
	}
	x, err := toFloat(args[0], "math.ceil")
	if err != nil {
		return vm.Null, err
	}
	return vm.IntValue(int64(math.Ceil(x))), nil
}

func mathFloor(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 {
		return vm.Null, fmt.Errorf("math.floor: requires 1 argument")
	}
	x, err := toFloat(args[0], "math.floor")
	if err != nil {
		return vm.Null, err
	}
	return vm.IntValue(int64(math.Floor(x))), nil
}

// mathLog computes log(base, n). Buzz's signature: fun log(double base, double n) > double.
func mathLog(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 2 {
		return vm.Null, fmt.Errorf("math.log: requires 2 arguments (base, n)")
	}
	base, err := toFloat(args[0], "math.log")
	if err != nil {
		return vm.Null, err
	}
	n, err := toFloat(args[1], "math.log")
	if err != nil {
		return vm.Null, err
	}
	return vm.FloatValue(math.Log(n) / math.Log(base)), nil
}

func mathPow(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 2 {
		return vm.Null, fmt.Errorf("math.pow: requires 2 arguments (x, y)")
	}
	x, err := toFloat(args[0], "math.pow")
	if err != nil {
		return vm.Null, err
	}
	y, err := toFloat(args[1], "math.pow")
	if err != nil {
		return vm.Null, err
	}
	result := math.Pow(x, y)
	if math.IsInf(result, 1) {
		return vm.Null, fmt.Errorf("math.pow: overflow")
	}
	if result == 0 && (x != 0 || y >= 0) {
		return vm.Null, fmt.Errorf("math.pow: underflow")
	}
	return vm.FloatValue(result), nil
}
