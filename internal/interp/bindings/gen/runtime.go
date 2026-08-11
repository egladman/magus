// runtime.go holds the hand-maintained VM conversion primitives the generated
// module trampolines call into. Do not add generated code here.

package gen

import (
	"context"
	"errors"
	"reflect"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// --- arg decoders (0-indexed; a missing or wrong-typed arg yields the zero value) ---

// Str reads arg n as a string, "" if absent or not a string.
func Str(args []vm.Value, n int) string {
	if n >= len(args) {
		return ""
	}
	v := args[n]
	// A str-backed ENUM case crosses as the case's value. The compiler lowers both
	// `Enum.case` and an inferred `.case` to the enum member, so a host method
	// declaring an enum argument receives the enum value rather than a str - and
	// without this it read as "" and the host reported the argument as unset. That is
	// the failure mode vm.Value.EnumValue's own doc names: an enum decoding to nothing,
	// silently, which is the thing a typed enum was adopted to prevent.
	if inner, isEnum := v.EnumValue(); isEnum {
		v = inner
	}
	if v.IsStr() {
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
// ByteSlice converts argument n to raw bytes, accepting either a str (taken as
// its bytes) or a list of ints.
//
// BOTH forms, because the two are how bytes reach a host method from either
// direction: a literal payload is written as a str, while anything a previous
// host call produced (an HMAC digest, a base64 decode) is a byte list, and an
// AWS SigV4 signing chain feeds one straight back as the key of the next.
func ByteSlice(args []vm.Value, n int) []byte {
	if n >= len(args) {
		return nil
	}
	v := args[n]
	if v.IsStr() {
		return []byte(v.AsString())
	}
	if !v.IsList() {
		return nil
	}
	items := v.ListItems()
	out := make([]byte, 0, len(items))
	for _, it := range items {
		if it.IsInt() {
			out = append(out, byte(it.AsInt()))
		}
	}
	return out
}

// ByteSliceVal converts raw bytes to a Buzz list of ints.
//
// A list rather than a str: a Buzz string is rune-oriented, so a NUL or a 0xFF
// does not survive a round trip through one, and a digest that lost a byte would
// be wrong rather than obviously broken.
func ByteSliceVal(b []byte) vm.Value {
	items := make([]vm.Value, len(b))
	for i, x := range b {
		items[i] = vm.IntValue(int64(x))
	}
	return vm.ListValue(items)
}

// StrSliceSlice converts argument n to [][]string, skipping non-list rows.
func StrSliceSlice(args []vm.Value, n int) [][]string {
	if n >= len(args) {
		return nil
	}
	v := args[n]
	if !v.IsList() {
		return nil
	}
	rows := v.ListItems()
	out := make([][]string, 0, len(rows))
	for _, row := range rows {
		if !row.IsList() {
			continue
		}
		out = append(out, strSliceFromValue(row))
	}
	return out
}

// StrSliceSliceVal converts [][]string to a Buzz list of lists.
func StrSliceSliceVal(rows [][]string) vm.Value {
	items := make([]vm.Value, len(rows))
	for i, row := range rows {
		items[i] = StrSliceVal(row)
	}
	return vm.ListValue(items)
}

// StrMapMap converts argument n to map[string]map[string]string, skipping
// entries whose value is not itself a map.
func StrMapMap(args []vm.Value, n int) map[string]map[string]string {
	if n >= len(args) {
		return nil
	}
	v := args[n]
	if !v.IsMap() {
		return nil
	}
	out := map[string]map[string]string{}
	for _, k := range v.MapKeys() {
		inner, ok := v.MapGet(k)
		if !ok || !inner.IsMap() {
			continue
		}
		section := map[string]string{}
		for _, ik := range inner.MapKeys() {
			iv, ok := inner.MapGet(ik)
			if !ok {
				continue
			}
			if iv.IsStr() {
				section[ik] = iv.AsString()
			} else {
				section[ik] = iv.String()
			}
		}
		out[k] = section
	}
	return out
}

// StrMapMapVal converts map[string]map[string]string to a Buzz map of maps.
func StrMapMapVal(m map[string]map[string]string) vm.Value {
	out := vm.NewMap()
	for k, inner := range m {
		out.MapSet(k, StrMapVal(inner))
	}
	return out
}

// FloatSlice converts argument n to []float64, skipping non-numeric items.
//
// Buzz has ONE numeric list: an int list and a double list are the same value
// here, so both cross as float64 and a caller meaning integers rounds on the way
// out. A non-numeric item is skipped rather than becoming 0, because a silent
// zero would shift a mean or a median without anything reporting it.
func FloatSlice(args []vm.Value, n int) []float64 {
	if n >= len(args) {
		return nil
	}
	v := args[n]
	if !v.IsList() {
		return nil
	}
	items := v.ListItems()
	out := make([]float64, 0, len(items))
	for _, it := range items {
		switch {
		case it.IsInt():
			out = append(out, float64(it.AsInt()))
		case it.IsFloat():
			out = append(out, it.AsFloat())
		}
	}
	return out
}

// FloatSliceVal converts []float64 to a Buzz list.
func FloatSliceVal(f []float64) vm.Value {
	items := make([]vm.Value, len(f))
	for i, v := range f {
		items[i] = vm.FloatValue(v)
	}
	return vm.ListValue(items)
}

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
	// types.BuzzObject is a NAMED map[string]any, and a type switch matches on type
	// IDENTITY, not on underlying type - so without its own case it falls through to
	// the null below. That is not theoretical: it is what broke every NESTED boundary
	// value the moment the named type was introduced. Tag.BuzzObject() nests
	// Version.BuzzObject(), whose Go type is BuzzObject, so `t.version` arrived in a
	// magusfile as null and every field read off it read null too.
	case types.BuzzObject:
		return AnyMapVal(x)
	case map[string]string:
		return StrMapVal(x)
	}
	// A DEFINED type over a basic kind - types.DoctorCheckStatus, types.TargetRunState,
	// spells.PatchOpKind - matches none of the cases above, because a type switch
	// matches on identity and not on underlying type. That is the same trap the
	// BuzzObject case documents, and it had the same symptom one layer down: a
	// generated BuzzObject emits `v.Status` typed, so `doctor().checks[0].status` read
	// as NULL in a magusfile rather than "ok" - and a caller told to branch on status
	// instead of grepping console text was branching on nothing.
	//
	// Handled reflectively rather than by naming each type, because the failure is
	// silent and the next defined type would reintroduce it. Reflection is confined to
	// this fallthrough, which the cases above have already skipped.
	return namedBasicVal(v)
}

// namedBasicVal converts a defined type whose underlying kind is basic. Anything else
// stays null, which is the honest answer for a shape the boundary cannot represent.
func namedBasicVal(v any) vm.Value {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return vm.Null
	}
	switch rv.Kind() {
	case reflect.String:
		return vm.StrValue(rv.String())
	case reflect.Bool:
		return vm.BoolValue(rv.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return vm.IntValue(rv.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return vm.IntValue(int64(rv.Uint()))
	case reflect.Float32, reflect.Float64:
		return vm.FloatValue(rv.Float())
	}
	return vm.Null
}

// valToAny converts a Buzz Value to a plain Go value for host consumption.
func valToAny(v vm.Value) any {
	// An enum case converts to its backing value, so a host reading a Buzz record
	// sees "add" where the author wrote PatchOpKind.add. Ahead of the type switch
	// because an enum case matches none of the scalar predicates: without this it
	// falls through to the default and the field silently arrives empty.
	if ev, ok := v.EnumValue(); ok {
		v = ev
	}
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
		// An object instance (e.g. a Run/Charm/PatchOp literal a spell builds)
		// marshals to its {field: value} map via MapView.
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
// Exported for host code outside this package that marshals across the boundary
// (e.g. spell function-op Params); it shares one implementation with the generated
// trampolines so the two can't drift.
func AnyToValue(v any) vm.Value { return AnyVal(v) }

// ValueToAny converts a Buzz Value to a plain Go value. The inverse of
// [AnyToValue]; see its note for why this is exported.
func ValueToAny(v vm.Value) any { return valToAny(v) }

// buzzObject is the narrow boundary view host needs. It stays at the consumer:
// types define their exact BuzzObject representation without publishing a
// speculative interface for callers that do not need polymorphism.
type buzzObject interface{ BuzzObject() types.BuzzObject }

// MapsVal marshals a slice of field-objects to a Buzz list of their boundary maps,
// the return form for list-of-object Impls like vcs.history. It keeps the "Maps"
// name (not "ObjectsVal") because it names the vm.Value SHAPE it produces - a
// Buzz list of maps - matching AnyMapVal/StrMapVal's convention of naming after
// the runtime value, not the source type's Buzz-language name.
func MapsVal[T buzzObject](rs []T) vm.Value {
	items := make([]vm.Value, len(rs))
	for i, r := range rs {
		items[i] = AnyMapVal(r.BuzzObject())
	}
	return vm.ListValue(items)
}

// buzzCallback implements std.Callback for a Buzz function value.
type buzzCallback struct {
	sess *buzz.Session
	fn   vm.Value
}

// Call invokes the Buzz function and returns its result as a plain Go value.
// Predicate helpers (fs.walk, charm.*_func, ...) derive truthiness from it via
// callbackTruthy/callPredicate (nil/false -> false, anything else -> true), so
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

// HostError wraps an error on its way from a host method into the VM so a magusfile
// always catches the same SHAPE.
//
// gopherbuzz deliberately leaves a plain error as a string, because upstream Buzz does and
// its conformance fixtures pin that; enriching is the embedder's opt-in. This is magus
// taking it. Without it magus would have a two-shape error surface - coded diagnostics
// arriving as maps, everything else as text - and an author would have to know which calls
// raise which before knowing whether e["code"] is safe to read. That is the kind of thing
// you memorise instead of learn.
//
// A diagnostic keeps its own fields (code, url); anything else gets message alone. Every
// generated trampoline routes its error through here, so the guarantee holds by
// construction rather than by remembering.
func HostError(err error) error {
	if err == nil {
		return nil
	}
	var se vm.StructuredError
	if errors.As(err, &se) {
		return err // already carries its own fields
	}
	return hostError{err}
}

// hostError gives a bare error the minimum structured shape: just a message.
type hostError struct{ err error }

func (e hostError) Error() string { return e.err.Error() }
func (e hostError) Unwrap() error { return e.err }
func (e hostError) BuzzError() map[string]string {
	return map[string]string{"message": e.err.Error()}
}
