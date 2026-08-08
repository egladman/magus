package vm

import (
	"context"
	"fmt"

	"github.com/dlclark/regexp2"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
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

// patMatchDef is the object type behind upstream's match records. At UpstreamRef
// the two match methods return `[obj{ capture: str, start: int, end: int }]?`, an
// ANONYMOUS object type; gopherbuzz has no anonymous-object runtime value, so one
// shared named def stands in for it. Field access resolves by name through
// Def.fieldIndex, so `.capture` reads the same either way, and the def is only
// ever read (instances are immutable, Mut stays false), which is what makes one
// package-level def safe to share across VMs.
var patMatchDef = &objectDefObj{
	Name: "Match",
	Fields: []ast.ObjField{
		{Name: "capture", TypeAnnot: "str"},
		{Name: "start", TypeAnnot: "int"},
		{Name: "end", TypeAnnot: "int"},
	},
}

// matchToRecords renders a regexp2 match the way upstream's matchAgainst does:
// index 0 is the whole match, the rest are capture groups in order, each carrying
// its text and its offsets in subject.
//
// regexp2 indexes in RUNES (it matches over []rune) while upstream reports BYTE
// offsets, so the index is converted rather than passed through -- they agree on
// ASCII and diverge on anything else, which is exactly the kind of silent
// difference this suite exists to catch.
func (vm *VM) matchToRecords(subject string, m *regexp2.Match) Value {
	groups := m.Groups()
	runes := []rune(subject)
	items := make([]Value, len(groups))
	for i, g := range groups {
		text := g.String()
		// A group that did not participate in the match has no position; report it
		// at offset zero rather than indexing out of range.
		start := 0
		if g.Index >= 0 && g.Index <= len(runes) {
			start = len(string(runes[:g.Index]))
		}
		items[i] = vm.allocObject(&objectInst{
			Def:    patMatchDef,
			Fields: []Value{StrValue(text), IntValue(int64(start)), IntValue(int64(start + len(text)))},
		})
	}
	return ListValue(items)
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

// patMethod returns the callable for the named pat method, or nil if name is not
// a known method. Mirrors listMethod/mapMethod in operators.go.
func patMethod(vm *VM, p Value, name string) *directObj {
	po := vm.asPat(p)
	switch name {
	case "match":
		return newDirect("pat.match", func(_ context.Context, args []Value) (Value, error) {
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
		return newDirect("pat.matchAll", func(_ context.Context, args []Value) (Value, error) {
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
	// matchAgainst/matchAllAgainst are upstream's names at UpstreamRef, where they
	// replaced match/matchAll and changed the element type from a bare str to a
	// record carrying the text plus its offsets. match/matchAll are kept above as a
	// gopherbuzz superset (they are what its own testdata calls); upstream source
	// only ever reaches these two.
	case "matchAgainst":
		return newDirect("pat.matchAgainst", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("pat.matchAgainst: requires a subject string")
			}
			subject := args[0].AsString()
			m, err := po.re.FindStringMatch(subject)
			if err != nil {
				return Null, fmt.Errorf("pat.matchAgainst: %w", err)
			}
			if m == nil {
				return Null, nil // no match -> null (the result type is optional)
			}
			return vm.matchToRecords(subject, m), nil
		})
	case "matchAllAgainst":
		return newDirect("pat.matchAllAgainst", func(_ context.Context, args []Value) (Value, error) {
			if len(args) < 1 {
				return Null, fmt.Errorf("pat.matchAllAgainst: requires a subject string")
			}
			subject := args[0].AsString()
			m, err := po.re.FindStringMatch(subject)
			if err != nil {
				return Null, fmt.Errorf("pat.matchAllAgainst: %w", err)
			}
			if m == nil {
				return Null, nil // no matches -> null (the result type is optional)
			}
			var all []Value
			for m != nil {
				all = append(all, vm.matchToRecords(subject, m))
				if m, err = po.re.FindNextMatch(m); err != nil {
					return Null, fmt.Errorf("pat.matchAllAgainst: %w", err)
				}
			}
			return ListValue(all), nil
		})
	case "replace":
		return newDirect("pat.replace", func(_ context.Context, args []Value) (Value, error) {
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
		return newDirect("pat.replaceAll", func(_ context.Context, args []Value) (Value, error) {
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
	return nil
}
