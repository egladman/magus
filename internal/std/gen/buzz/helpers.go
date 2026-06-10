// helpers.go — intentionally hand-maintained: shared conversion primitives that
// generated trampolines call into. Do not add generated code here. (The package
// doc lives in buzzgen.go.)

package buzzgen

import (
	"context"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/std"
)

// --- arg decoders (0-indexed; a missing or wrong-typed arg yields the zero value) ---

// bzStr reads arg n as a string, "" if absent or not a string.
func bzStr(args []buzz.Value, n int) string {
	if n >= len(args) {
		return ""
	}
	if v := args[n]; v.IsStr() {
		return v.AsString()
	}
	return ""
}

// bzInt reads arg n as an int (accepting int or float), def if absent.
func bzInt(args []buzz.Value, n, def int) int {
	if n >= len(args) {
		return def
	}
	switch v := args[n]; {
	case v.IsInt():
		return int(v.AsInt())
	case v.IsFloat():
		return int(v.AsFloat())
	default:
		return def
	}
}

// bzFloat reads arg n as a float64, accepting both int and float Buzz values.
func bzFloat(args []buzz.Value, n int, def float64) float64 {
	if n >= len(args) {
		return def
	}
	switch v := args[n]; {
	case v.IsFloat():
		return v.AsFloat()
	case v.IsInt():
		return float64(v.AsInt())
	default:
		return def
	}
}

// bzBool reads arg n as a bool, def if absent or not a bool.
func bzBool(args []buzz.Value, n int, def bool) bool {
	if n >= len(args) {
		return def
	}
	if v := args[n]; v.IsBool() {
		return v.AsBool()
	}
	return def
}

// bzStrSlice reads arg n as []string, nil if absent or not a list.
func bzStrSlice(args []buzz.Value, n int) []string {
	if n >= len(args) {
		return nil
	}
	return bzStrSliceFromValue(args[n])
}

// bzStrSliceFromValue converts a Buzz list to []string, stringifying non-string
// items. Returns nil if v is not a list.
func bzStrSliceFromValue(v buzz.Value) []string {
	if !v.IsList() {
		return nil
	}
	items := v.ListItems()
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.IsStr() {
			out = append(out, it.AsString())
		} else {
			out = append(out, it.String())
		}
	}
	return out
}

// bzStrMap reads arg n as map[string]string, nil if absent or not a map.
func bzStrMap(args []buzz.Value, n int) map[string]string {
	if n >= len(args) {
		return nil
	}
	v := args[n]
	if !v.IsMap() {
		return nil
	}
	out := map[string]string{}
	for _, k := range v.MapKeys() {
		mv, ok := v.MapGet(k)
		if !ok {
			continue
		}
		if mv.IsStr() {
			out[k] = mv.AsString()
		} else {
			out[k] = mv.String()
		}
	}
	return out
}

// bzAnyMap reads arg n as map[string]any, nil if absent or not a map.
func bzAnyMap(args []buzz.Value, n int) map[string]any {
	if n >= len(args) {
		return nil
	}
	v := args[n]
	if !v.IsMap() {
		return nil
	}
	out := map[string]any{}
	for _, k := range v.MapKeys() {
		if mv, ok := v.MapGet(k); ok {
			out[k] = bzValToAny(mv)
		}
	}
	return out
}

// bzAny reads arg n as a plain Go value, nil if absent.
func bzAny(args []buzz.Value, n int) any {
	if n >= len(args) {
		return nil
	}
	return bzValToAny(args[n])
}

// bzVariadicStr collects args from index n onward as []string.
func bzVariadicStr(args []buzz.Value, n int) []string {
	if n >= len(args) {
		return nil
	}
	out := make([]string, 0, len(args)-n)
	for _, v := range args[n:] {
		if v.IsStr() {
			out = append(out, v.AsString())
		} else {
			out = append(out, v.String())
		}
	}
	return out
}

// bzCallback wraps arg n (a Buzz function value) as a std.Callback, nil if
// absent or not callable.
func bzCallback(sess *buzz.Session, args []buzz.Value, n int) std.Callback {
	if n >= len(args) {
		return nil
	}
	if v := args[n]; v.IsFun() {
		return &buzzCallback{sess: sess, fn: v}
	}
	return nil
}

// --- return-value encoders ---

func bzStrVal(s string) buzz.Value    { return buzz.StrValue(s) }
func bzIntVal(i int) buzz.Value       { return buzz.IntValue(int64(i)) }
func bzBoolVal(b bool) buzz.Value     { return buzz.BoolValue(b) }
func bzFloatVal(f float64) buzz.Value { return buzz.FloatValue(f) }

// bzStrSliceVal converts []string to a Buzz list.
func bzStrSliceVal(s []string) buzz.Value {
	items := make([]buzz.Value, len(s))
	for i, v := range s {
		items[i] = buzz.StrValue(v)
	}
	return buzz.ListValue(items)
}

// bzStrMapVal converts map[string]string to a Buzz map.
func bzStrMapVal(m map[string]string) buzz.Value {
	out := buzz.NewMap()
	for k, v := range m {
		out.MapSet(k, buzz.StrValue(v))
	}
	return out
}

// bzAnyMapVal converts map[string]any to a Buzz map.
func bzAnyMapVal(m map[string]any) buzz.Value {
	out := buzz.NewMap()
	for k, v := range m {
		out.MapSet(k, bzAnyVal(v))
	}
	return out
}

// bzAnyVal converts a Go value to a Buzz Value; unknown types become null.
func bzAnyVal(v any) buzz.Value {
	switch x := v.(type) {
	case nil:
		return buzz.Null
	case string:
		return buzz.StrValue(x)
	case bool:
		return buzz.BoolValue(x)
	case int:
		return buzz.IntValue(int64(x))
	case int64:
		return buzz.IntValue(x)
	case float64:
		return buzz.FloatValue(x)
	case []string:
		return bzStrSliceVal(x)
	case []any:
		items := make([]buzz.Value, len(x))
		for i, vv := range x {
			items[i] = bzAnyVal(vv)
		}
		return buzz.ListValue(items)
	case map[string]any:
		return bzAnyMapVal(x)
	case map[string]string:
		return bzStrMapVal(x)
	}
	return buzz.Null
}

// bzValToAny converts a Buzz Value to a plain Go value for host consumption.
func bzValToAny(v buzz.Value) any {
	switch {
	case v.IsBool():
		return v.AsBool()
	case v.IsInt():
		return v.AsInt()
	case v.IsFloat():
		return v.AsFloat()
	case v.IsStr():
		return v.AsString()
	case v.IsList():
		items := v.ListItems()
		out := make([]any, len(items))
		for i, it := range items {
			out[i] = bzValToAny(it)
		}
		return out
	case v.IsMap():
		out := map[string]any{}
		for _, k := range v.MapKeys() {
			if mv, ok := v.MapGet(k); ok {
				out[k] = bzValToAny(mv)
			}
		}
		return out
	}
	return nil
}

// AnyToValue converts a Go value to a Buzz Value (unknown types become null).
// Exported for host code outside this package that marshals across the boundary,
// e.g. spell function-op Params; it shares one implementation with the generated
// trampolines so the two can't drift.
func AnyToValue(v any) buzz.Value { return bzAnyVal(v) }

// ValueToAny converts a Buzz Value to a plain Go value. The inverse of
// [AnyToValue]; see its note for why this is exported.
func ValueToAny(v buzz.Value) any { return bzValToAny(v) }

// buzzCallback implements std.Callback for a Buzz function value.
type buzzCallback struct {
	sess *buzz.Session
	fn   buzz.Value
}

// Call invokes the Buzz function and reports its result's truthiness — the shape
// host predicate helpers (arg.index_func, …) expect. Void callbacks ignore it.
func (c *buzzCallback) Call(ctx context.Context, args ...any) ([]any, error) {
	bargs := make([]buzz.Value, len(args))
	for i, a := range args {
		bargs[i] = bzAnyVal(a)
	}
	res, err := c.sess.CallValue(ctx, c.fn, bargs)
	if err != nil {
		return nil, err
	}
	return []any{res.Bool()}, nil
}
