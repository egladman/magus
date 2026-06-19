// helpers.go — intentionally hand-maintained: shared conversion primitives that
// the generated trampolines (the gen subpackage) call into. Do not add generated
// code here. (The package doc lives in doc.go.)

package host

import (
	"context"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/egladman/magus/std"
)

// --- arg decoders (0-indexed; a missing or wrong-typed arg yields the zero value) ---

// Str reads arg n as a string, "" if absent or not a string.
func Str(args []vm.Value, n int) string {
	if n >= len(args) {
		return ""
	}
	if v := args[n]; v.IsStr() {
		return v.AsString()
	}
	return ""
}

// Int reads arg n as an int (accepting int or float), def if absent.
func Int(args []vm.Value, n, def int) int {
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

// Float reads arg n as a float64, accepting both int and float Buzz values.
func Float(args []vm.Value, n int, def float64) float64 {
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

// Bool reads arg n as a bool, def if absent or not a bool.
func Bool(args []vm.Value, n int, def bool) bool {
	if n >= len(args) {
		return def
	}
	if v := args[n]; v.IsBool() {
		return v.AsBool()
	}
	return def
}

// StrSlice reads arg n as []string, nil if absent or not a list.
func StrSlice(args []vm.Value, n int) []string {
	if n >= len(args) {
		return nil
	}
	return strSliceFromValue(args[n])
}

// strSliceFromValue converts a Buzz list to []string, stringifying non-string
// items. Returns nil if v is not a list.
func strSliceFromValue(v vm.Value) []string {
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

// StrMap reads arg n as map[string]string, nil if absent or not a map.
func StrMap(args []vm.Value, n int) map[string]string {
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

// AnyMap reads arg n as map[string]any, nil if absent or not a map.
func AnyMap(args []vm.Value, n int) map[string]any {
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
			out[k] = valToAny(mv)
		}
	}
	return out
}

// Any reads arg n as a plain Go value, nil if absent.
func Any(args []vm.Value, n int) any {
	if n >= len(args) {
		return nil
	}
	return valToAny(args[n])
}

// VariadicStr collects args from index n onward as []string.
func VariadicStr(args []vm.Value, n int) []string {
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

// CallbackArg wraps arg n (a Buzz function value) as a std.Callback, nil if
// absent or not callable.
func CallbackArg(sess *buzz.Session, args []vm.Value, n int) std.Callback {
	if n >= len(args) {
		return nil
	}
	if v := args[n]; v.IsFun() {
		return &buzzCallback{sess: sess, fn: v}
	}
	return nil
}

func StrVal(s string) vm.Value    { return vm.StrValue(s) }
func IntVal(i int) vm.Value       { return vm.IntValue(int64(i)) }
func BoolVal(b bool) vm.Value     { return vm.BoolValue(b) }
func FloatVal(f float64) vm.Value { return vm.FloatValue(f) }

// StrSliceVal converts []string to a Buzz list.
func StrSliceVal(s []string) vm.Value {
	items := make([]vm.Value, len(s))
	for i, v := range s {
		items[i] = vm.StrValue(v)
	}
	return vm.ListValue(items)
}

// StrMapVal converts map[string]string to a Buzz map.
func StrMapVal(m map[string]string) vm.Value {
	out := vm.NewMap()
	for k, v := range m {
		out.MapSet(k, vm.StrValue(v))
	}
	return out
}

// AnyMapVal converts map[string]any to a Buzz map.
func AnyMapVal(m map[string]any) vm.Value {
	out := vm.NewMap()
	for k, v := range m {
		out.MapSet(k, AnyVal(v))
	}
	return out
}

// AnyVal converts a Go value to a Buzz Value; unknown types become null.
func AnyVal(v any) vm.Value {
	switch x := v.(type) {
	case nil:
		return vm.Null
	case string:
		return vm.StrValue(x)
	case bool:
		return vm.BoolValue(x)
	case int:
		return vm.IntValue(int64(x))
	case int64:
		return vm.IntValue(x)
	case float64:
		return vm.FloatValue(x)
	case []string:
		return StrSliceVal(x)
	case []any:
		items := make([]vm.Value, len(x))
		for i, vv := range x {
			items[i] = AnyVal(vv)
		}
		return vm.ListValue(items)
	case map[string]any:
		return AnyMapVal(x)
	case map[string]string:
		return StrMapVal(x)
	}
	return vm.Null
}

// valToAny converts a Buzz Value to a plain Go value for host consumption.
func valToAny(v vm.Value) any {
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
			out[i] = valToAny(it)
		}
		return out
	case v.IsMap():
		out := map[string]any{}
		for _, k := range v.MapKeys() {
			if mv, ok := v.MapGet(k); ok {
				out[k] = valToAny(mv)
			}
		}
		return out
	case v.IsObject():
		// An object instance (e.g. a Run/Charm/PatchOp literal a spell builds) marshals
		// to its field map; MapView yields the {field: value} view.
		mv, ok := v.MapView()
		if !ok {
			return nil
		}
		out := map[string]any{}
		for _, k := range mv.MapKeys() {
			if val, ok := mv.MapGet(k); ok {
				out[k] = valToAny(val)
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
func AnyToValue(v any) vm.Value { return AnyVal(v) }

// ValueToAny converts a Buzz Value to a plain Go value. The inverse of
// [AnyToValue]; see its note for why this is exported.
func ValueToAny(v vm.Value) any { return valToAny(v) }

// Recorder is a host value that marshals to its Buzz boundary map via Record.
// The typed record returns (types.ExecResult, types.FileInfo, types.Commit, …)
// implement it; the generated trampolines call Record so a magusfile sees the
// same {field: value} map the Impl used to return directly.
type Recorder interface{ Record() map[string]any }

// RecordsVal marshals a slice of records to a Buzz list of their boundary maps —
// the return form for list-of-record Impls like vcs.history.
func RecordsVal[T Recorder](rs []T) vm.Value {
	items := make([]vm.Value, len(rs))
	for i, r := range rs {
		items[i] = AnyMapVal(r.Record())
	}
	return vm.ListValue(items)
}

// buzzCallback implements std.Callback for a Buzz function value.
type buzzCallback struct {
	sess *buzz.Session
	fn   vm.Value
}

// Call invokes the Buzz function and returns its result as a plain Go value.
// Predicate helpers (fs.walk, charm.*_func, …) derive truthiness from it via
// callbackTruthy/callPredicate (nil/false → false, any other value → true), so
// returning the marshalled value rather than a pre-reduced bool keeps those
// callers correct while also letting value-returning callbacks (os.retry, which
// hands back fn's result on success) see what the callback actually produced.
// Void callbacks ignore the return.
func (c *buzzCallback) Call(ctx context.Context, args ...any) ([]any, error) {
	bargs := make([]vm.Value, len(args))
	for i, a := range args {
		bargs[i] = AnyVal(a)
	}
	res, err := c.sess.CallValue(ctx, c.fn, bargs)
	if err != nil {
		return nil, err
	}
	return []any{valToAny(res)}, nil
}
