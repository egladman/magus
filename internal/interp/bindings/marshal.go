package bindings

// Buzz → Go argument marshaling for host bindings.
//
// Every magus.* / extra.* host function receives []buzzeng.Value and must pull
// typed Go values out of positional arguments. Done inline, that is the same
// `if len(args) > i && args[i].IsStr() { x = args[i].AsString() }` dance repeated
// across dozens of call sites. These accessors centralize that boundary — the
// same move magus/gopherbuzz's FFI layer makes with buzzToReflectArg /
// reflectRetToValue, where one typed conversion site replaces per-call
// hand-marshaling (see magus/gopherbuzz/doc/ffi.md). The Go→Buzz direction
// already has its counterparts here (strSliceToBuzzList, execRecordToBuzz, …).
//
// All accessors are deliberately lenient: an out-of-range index or wrong-typed
// argument yields the zero value, which is exactly how the bindings already
// treat optional arguments (a missing message logs "", a missing path registers
// nothing). A binding that must *reject* bad input keeps validating explicitly
// rather than reaching for these.

import buzzeng "github.com/egladman/gopherbuzz"

// argStr reads positional argument i as a string, or "" if absent or not a str.
func argStr(args []buzzeng.Value, i int) string {
	if i >= 0 && i < len(args) && args[i].IsStr() {
		return args[i].AsString()
	}
	return ""
}

// argStrSlice reads positional argument i as a []string, accepting either a
// single str or a list of strs (the "one or many" shape host functions take).
// Returns nil if absent or neither str nor list.
func argStrSlice(args []buzzeng.Value, i int) []string {
	if i >= 0 && i < len(args) {
		return buzzValToStringSlice(args[i])
	}
	return nil
}

// argStrMap reads positional argument i as a map[string]string, or nil if absent
// or not a map. Map values are taken via AsString (matching existing call sites).
func argStrMap(args []buzzeng.Value, i int) map[string]string {
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
