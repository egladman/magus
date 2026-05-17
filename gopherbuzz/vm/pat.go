package vm

import (
	"context"
	"fmt"

	"github.com/dlclark/regexp2"
)

// patObj is a compiled Buzz pattern (the `pat` type) — a PCRE-style regular
// expression written as a $"..." literal. src is the original pattern source
// (kept for String() and bytecode marshalling); re is the matcher compiled by
// the pure-Go regexp2 engine, which supports PCRE constructs (backreferences,
// lookaround) the stdlib RE2-based regexp package rejects. Compiling here rather
// than via cgo keeps the interpreter pure Go and WASM-portable.
type patObj struct {
	src string
	re  *regexp2.Regexp
}

func (*patObj) heapKind() valueTag { return tagPat }

// PatValue compiles src into a Buzz pattern value. A malformed pattern returns
// an error, which the compiler surfaces at the pattern literal's source location.
func PatValue(src string) (Value, error) {
	re, err := regexp2.Compile(src, regexp2.None)
	if err != nil {
		return Null, fmt.Errorf("buzz: invalid pattern %q: %w", src, err)
	}
	return heapValue(tagPat, &patObj{src: src, re: re}), nil
}

// matchToList renders a regexp2 match as a Buzz [str]: index 0 is the whole
// match, the rest are capture groups in order.
func matchToList(m *regexp2.Match) Value {
	groups := m.Groups()
	items := make([]Value, len(groups))
	for i, g := range groups {
		items[i] = StrValue(g.String())
	}
	return ListValue(items)
}

// patMethod returns a bound DirectValue for the named pat method, or Null if
// name is not a known method. Mirrors listMethod/mapMethod in operators.go.
func patMethod(vm *VM, p Value, name string) Value {
	po := vm.asPat(p)
	switch name {
	case "match":
		return DirectValue("pat.match", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("pat.match: requires a subject string")
			}
			m, err := po.re.FindStringMatch(args[0].AsString())
			if err != nil {
				return Null, fmt.Errorf("pat.match: %w", err)
			}
			if m == nil {
				return Null, nil // no match → null (the result type is [str]?)
			}
			return matchToList(m), nil
		})
	case "matchAll":
		return DirectValue("pat.matchAll", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("pat.matchAll: requires a subject string")
			}
			m, err := po.re.FindStringMatch(args[0].AsString())
			if err != nil {
				return Null, fmt.Errorf("pat.matchAll: %w", err)
			}
			if m == nil {
				return Null, nil // no matches → null (the result type is [[str]]?)
			}
			var all []Value
			for m != nil {
				all = append(all, matchToList(m))
				if m, err = po.re.FindNextMatch(m); err != nil {
					return Null, fmt.Errorf("pat.matchAll: %w", err)
				}
			}
			return ListValue(all), nil
		})
	case "replace":
		return DirectValue("pat.replace", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 2 {
				return Null, fmt.Errorf("pat.replace: requires subject and replacement strings")
			}
			// count=1: replace only the first occurrence.
			out, err := po.re.Replace(args[0].AsString(), args[1].AsString(), -1, 1)
			if err != nil {
				return Null, fmt.Errorf("pat.replace: %w", err)
			}
			return StrValue(out), nil
		})
	case "replaceAll":
		return DirectValue("pat.replaceAll", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 2 {
				return Null, fmt.Errorf("pat.replaceAll: requires subject and replacement strings")
			}
			// count=-1: replace every occurrence.
			out, err := po.re.Replace(args[0].AsString(), args[1].AsString(), -1, -1)
			if err != nil {
				return Null, fmt.Errorf("pat.replaceAll: %w", err)
			}
			return StrValue(out), nil
		})
	}
	return Null
}
