package bindings

import "github.com/egladman/magus/libs/gopherbuzz/vm"

// Buzz → Go argument marshaling for host bindings.
//
// Every magus.* / extra.* host function receives []buzz.Value and must pull
// typed Go values out of positional arguments. Done inline, that is the same
// `if len(args) > i && args[i].IsStr() { x = args[i].AsString() }` dance repeated
// across dozens of call sites. These accessors centralize that boundary — the
// same move magus/gopherbuzz's FFI layer makes with buzzToReflectArg /
// reflectRetToValue, where one typed conversion site replaces per-call
// hand-marshaling (see gopherbuzz/doc/ffi.md). The Go→Buzz direction
// already has its counterparts here (strSliceToBuzzList, execRecordToBuzz, …).
//
// All accessors are deliberately lenient: an out-of-range index or wrong-typed
// argument yields the zero value, which is exactly how the bindings already
// treat optional arguments (a missing message logs "", a missing path registers
// nothing). A binding that must *reject* bad input keeps validating explicitly
// rather than reaching for these.

// argStr reads positional argument i as a string, or "" if absent or not a str.
func argStr(args []vm.Value, i int) string {
	if i >= 0 && i < len(args) && args[i].IsStr() {
		return args[i].AsString()
	}
	return ""
}

// argStrMap reads positional argument i as a map[string]string, or nil if absent
// or not a map. Map values are taken via AsString (matching existing call sites).
func argStrMap(args []vm.Value, i int) map[string]string {
	if i < 0 || i >= len(args) || !args[i].IsMap() {
		return nil
	}
	out := map[string]string{}
	for _, k := range args[i].MapKeys() {
		if v, ok := args[i].MapGet(k); ok {
			out[k] = v.AsString()
		}
	}
	return out
}

// Go → Buzz: the inverse direction, turning typed Go values back into Buzz values.
// The richer Go→Buzz marshalers (execRecordToBuzz, targetsToBuzzMap, …) live next
// to the spell handles they build; these two are the generic primitives shared
// across every namespace.

// strSliceToBuzzList wraps a []string as a Buzz list of strs.
func strSliceToBuzzList(ss []string) vm.Value {
	items := make([]vm.Value, len(ss))
	for i, s := range ss {
		items[i] = vm.StrValue(s)
	}
	return vm.ListValue(items)
}

// buzzValToStringSlice reads a Buzz value as a []string, accepting either a single
// str or a list of strs (the "one or many" shape host functions take); other types
// yield nil.
func buzzValToStringSlice(v vm.Value) []string {
	switch {
	case v.IsStr():
		return []string{v.AsString()}
	case v.IsList():
		var out []string
		for _, item := range v.ListItems() {
			if item.IsStr() {
				out = append(out, item.AsString())
			}
		}
		return out
	}
	return nil
}
