package vm

import "strings"

// typeObj is a Buzz TYPE used as a value: what `<[str]>` denotes and what
// `typeof x` evaluates to. name is the canonical spelling of the type, and it is
// the whole payload - two type values are equal exactly when their canonical
// spellings match.
//
// A canonical spelling rather than a structural type graph is deliberate. Buzz's
// `typeof` is a STATIC operation: upstream compares the type DEFS the compiler
// resolved, so `final list = []` yields `[any]` while `final slist: [str] = []`
// yields `[str]` even though both are the same empty list at runtime. The VM
// therefore never inspects a value to answer `typeof`; the compiler hands it a
// constant the checker computed, and the VM only has to compare and print it.
// That makes the canonical string the entire contract, and it must be produced by
// exactly one function (canonicalTypeName in the checker) so the two sides of
// `typeof x == <T>` can never disagree on spacing.
type typeObj struct {
	name string
}

func (*typeObj) heapKind() valueTag { return tagType }

// TypeValue builds the Buzz type value whose canonical spelling is name.
func TypeValue(name string) Value { return heapValue(tagType, &typeObj{name: name}) }

// IsType reports whether v is a type value.
func (v Value) IsType() bool { return v.tag() == tagType }

// TypeName returns the canonical spelling of a type value, or "" for any other
// value.
func (v Value) TypeName() string {
	if v.tag() != tagType {
		return ""
	}
	return v.asType().name
}

// TypeShape reduces a source type annotation to the base name buzzIsType
// compares against, plus whether the annotation admits null. It runs at compile
// time (the compiler calls it) so OpIs stays a constant compare at runtime, and at
// run time for a type VALUE whose canonical spelling was never reduced (matchTest).
//
// Only the OUTER shape survives, because that is all a runtime tag can answer:
// `[int]` and `[str]` are both a list to a value that carries no element type.
// Element types are the checker's business, and it has the full annotation.
func TypeShape(annot string) (base string, nullable bool) {
	s := strings.TrimSpace(annot)
	s = strings.TrimPrefix(s, "mut ")
	s = strings.TrimSpace(s)
	if t := strings.TrimSuffix(s, "?"); t != s {
		s, nullable = strings.TrimSpace(t), true
	}
	switch {
	case strings.HasPrefix(s, "obj{"):
		// An anonymous object type is STRUCTURAL, so `x is obj{ name: str, age: int }`
		// asks whether x carries those fields. Types are erased at runtime, so the check
		// is field PRESENCE. The names are extracted here, once, into `obj{name,age}` --
		// OpIs runs in the hot dispatch switch and must not re-parse an annotation per
		// evaluation (the same reason the other shapes reduce here).
		inner := strings.TrimSuffix(strings.TrimPrefix(s, "obj{"), "}")
		var names []string
		// Bracket and angle nesting are tracked SEPARATELY. A function-type member
		// (`fun () > str`) contains a bare `>` return arrow with no opening `<`, so
		// counting it as a closer drove depth negative and made every later `,` fail
		// the split test -- silently dropping the remaining fields, which then made
		// `x is obj{ f: fun () > str, g: int }` answer true for a value with no `g`.
		// Only a `>` that closes an open `<` (a generic argument) counts.
		depth, angle, start := 0, 0, 0
		flush := func(end int) {
			field := inner[start:end]
			if name, _, found := strings.Cut(field, ":"); found {
				if name = strings.TrimSpace(name); name != "" {
					names = append(names, name)
				}
			}
			start = end + 1
		}
		for i := 0; i < len(inner); i++ {
			switch inner[i] {
			case '[', '{', '(':
				depth++
			case ']', '}', ')':
				depth--
			case '<':
				angle++
			case '>':
				if angle > 0 {
					angle--
				}
			case ',':
				if depth == 0 && angle == 0 {
					flush(i)
				}
			}
		}
		flush(len(inner))
		return "obj{" + strings.Join(names, ",") + "}", nullable
	case strings.HasPrefix(s, "["):
		return "list", nullable
	case strings.HasPrefix(s, "{"):
		return "map", nullable
	case s == "fun" || strings.HasPrefix(s, "fun ") || strings.HasPrefix(s, "fun("):
		// Function types carry no runtime signature, so every arity and return
		// type collapses to "is it callable".
		return "fun", nullable
	}
	if s := s[strings.LastIndex(s, `\`)+1:]; s != "" {
		// Namespace-qualified names (a\Hello) match on the declared name, which is
		// what an object or enum definition stores.
		if i := strings.IndexByte(s, '<'); i >= 0 {
			return s[:i], nullable // generic instantiation matches its base type
		}
		return s, nullable
	}
	return s, nullable
}
