package std

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"unicode/utf8"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
)

// This file and its companion ext_require.buzz are a magus-authored EXTENSION to
// Buzz, NOT part of upstream Buzz's standard library. They are installed by
// RegisterExtensions (never by RegisterWithOutput), so the upstream-mirrored std
// surface stays byte-for-byte faithful and the conformance suite is unaffected.
// The extension exists because Buzz's == is reference identity for maps, lists,
// and objects (matching upstream), so `{a: 1} == {a: 1}` is false and test code
// has no way to assert by value. deepEqual supplies the structural comparison a
// logic-less script can't express (an `any` is not field-accessible), and the
// require module layers readable, raise-on-failure assertions on top of it.

//go:embed ext_assert.buzz
var assertSource string

//go:embed ext_suite.buzz
var suiteSource string

//go:embed testing.buzz
var testingSource string

// RegisterExtensions installs the magus Buzz testing extensions on sess: the
// assertcore primitive module and the buzz-authored libraries that build on it -
// `assert` (matchers for `test` blocks), `suite` (a grouped, stateful test
// runner), and `testing` (gopherbuzz's own Tester, API-compatible with upstream
// Buzz's testing module; it uses assertcore for a runtime type name). Callers
// that want the upstream-faithful stdlib only should not call this; callers that
// want the magus testing surface call it alongside Register.
func RegisterExtensions(sess *buzz.Session) {
	sess.SetSyntheticModule("assertcore", assertCoreModule())
	sess.SetSourceModule("assert", assertSource)
	sess.SetSourceModule("suite", suiteSource)
	sess.SetSourceModule("testing", testingSource)
}

// assertCoreModule is the native primitive layer the buzz-authored require library
// imports. It exposes only what a logic-less script cannot do for itself:
// structural equality over an opaque `any`, and a type name for failure messages.
func assertCoreModule() vm.Value {
	m := mod()
	m.MapSet("deepEqual", fn("assertcore.deepEqual", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 2 {
			return vm.Null, fmt.Errorf("assertcore.deepEqual: requires 2 arguments")
		}
		return vm.BoolValue(deepEqualValue(args[0], args[1])), nil
	}))
	m.MapSet("typeName", fn("assertcore.typeName", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 {
			return vm.Null, fmt.Errorf("assertcore.typeName: requires 1 argument")
		}
		return vm.StrValue(typeNameValue(args[0])), nil
	}))
	// length returns the element count of a str (codepoints), list, or map; null
	// for a value that has no length, so the assert layer can raise a typed message.
	m.MapSet("length", fn("assertcore.length", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 1 {
			return vm.Null, fmt.Errorf("assertcore.length: requires 1 argument")
		}
		if n, ok := lengthValue(args[0]); ok {
			return vm.IntValue(int64(n)), nil
		}
		return vm.Null, nil
	}))
	// contains reports membership: a substring in a str, an element (by deep
	// equality) in a list, or a key in a map. False for any other container type.
	m.MapSet("contains", fn("assertcore.contains", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 2 {
			return vm.Null, fmt.Errorf("assertcore.contains: requires 2 arguments")
		}
		return vm.BoolValue(containsValue(args[0], args[1])), nil
	}))
	// compare orders two values: -1, 0, or 1 for two numbers (ints and doubles
	// compare across type) or two strings; null when they are not comparable.
	m.MapSet("compare", fn("assertcore.compare", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) < 2 {
			return vm.Null, fmt.Errorf("assertcore.compare: requires 2 arguments")
		}
		if c, ok := compareValue(args[0], args[1]); ok {
			return vm.IntValue(int64(c)), nil
		}
		return vm.Null, nil
	}))
	// skip aborts the current test by returning a sentinel-wrapped error. A test
	// runner recognizes it via SkipMessage and reports the test as skipped rather
	// than failed. The `assert` module's skip() is a thin wrapper over this.
	m.MapSet("skip", fn("assertcore.skip", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		msg := ""
		if len(args) > 0 && args[0].IsStr() {
			msg = args[0].AsString()
		}
		return vm.Null, fmt.Errorf("%s%s%s", skipPrefix, msg, skipSuffix)
	}))
	return m
}

// skipPrefix and skipSuffix bracket a skip reason inside the error a skipped test
// raises, so SkipMessage can recover it no matter how the VM wraps the error. The
// NUL bytes keep the marker from colliding with ordinary assertion text.
const (
	skipPrefix = "\x00gbtestskip:"
	skipSuffix = ":gbtestskip\x00"
)

// SkipMessage reports whether err is a test-skip signal (from assert\skip) and, if
// so, returns the skip reason. Test runners call it to classify a test-block error
// as skipped rather than failed.
func SkipMessage(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	s := err.Error()
	start := strings.Index(s, skipPrefix)
	if start < 0 {
		return "", false
	}
	rest := s[start+len(skipPrefix):]
	end := strings.Index(rest, skipSuffix)
	if end < 0 {
		return rest, true
	}
	return rest[:end], true
}

// lengthValue returns the element count of a measurable value (str codepoints,
// list items, map keys, or an object's fields) and whether it was measurable.
func lengthValue(v vm.Value) (int, bool) {
	switch {
	case v.IsStr():
		return utf8.RuneCountInString(v.AsString()), true
	case v.IsList():
		return len(v.ListItems()), true
	case v.IsMap():
		return len(v.MapKeys()), true
	case v.IsObject():
		if mv, ok := v.MapView(); ok {
			return len(mv.MapKeys()), true
		}
	}
	return 0, false
}

// containsValue reports whether container holds item: substring for strings,
// deep-equal element for lists, key for maps.
func containsValue(container, item vm.Value) bool {
	switch {
	case container.IsStr():
		return item.IsStr() && strings.Contains(container.AsString(), item.AsString())
	case container.IsList():
		for _, el := range container.ListItems() {
			if deepEqualValue(el, item) {
				return true
			}
		}
		return false
	case container.IsMap():
		if item.IsStr() {
			_, ok := container.MapGet(item.AsString())
			return ok
		}
	}
	return false
}

// compareValue orders two numbers (int/double cross-type) or two strings,
// returning -1/0/1 and whether the values were comparable.
func compareValue(a, b vm.Value) (int, bool) {
	switch {
	case (a.IsInt() || a.IsFloat()) && (b.IsInt() || b.IsFloat()):
		af, bf := numAsFloat(a), numAsFloat(b)
		switch {
		case af < bf:
			return -1, true
		case af > bf:
			return 1, true
		default:
			return 0, true
		}
	case a.IsStr() && b.IsStr():
		return strings.Compare(a.AsString(), b.AsString()), true
	}
	return 0, false
}

// deepEqualValue reports structural equality of two values. Maps match on key set
// and per-key value (order-independent); lists match element-wise in order; ints
// and doubles compare across type (1 equals 1.0), as Buzz's == does for scalars;
// objects compare by their field map, so an object equals a map with the same
// fields. Recurses to any depth.
func deepEqualValue(a, b vm.Value) bool {
	// Normalize object instances to their field map so structural comparison
	// works uniformly (and an object can equal an equivalent map literal).
	if a.IsObject() {
		if mv, ok := a.MapView(); ok {
			a = mv
		}
	}
	if b.IsObject() {
		if mv, ok := b.MapView(); ok {
			b = mv
		}
	}
	switch {
	case a.IsMap() && b.IsMap():
		ak := a.MapKeys()
		if len(ak) != len(b.MapKeys()) {
			return false
		}
		for _, k := range ak {
			av, _ := a.MapGet(k)
			bv, ok := b.MapGet(k)
			if !ok || !deepEqualValue(av, bv) {
				return false
			}
		}
		return true
	case a.IsList() && b.IsList():
		ai, bi := a.ListItems(), b.ListItems()
		if len(ai) != len(bi) {
			return false
		}
		for i := range ai {
			if !deepEqualValue(ai[i], bi[i]) {
				return false
			}
		}
		return true
	case a.IsMap() || b.IsMap() || a.IsList() || b.IsList():
		// One is a collection and the other is not.
		return false
	default:
		return scalarEqualValue(a, b)
	}
}

// scalarEqualValue compares two non-collection values by value: null==null,
// bool, string, and numbers with int/double compared across type.
func scalarEqualValue(a, b vm.Value) bool {
	switch {
	case a.IsNull() || b.IsNull():
		return a.IsNull() && b.IsNull()
	case a.IsBool() && b.IsBool():
		return a.AsBool() == b.AsBool()
	case a.IsStr() && b.IsStr():
		return a.AsString() == b.AsString()
	case a.IsInt() && b.IsInt():
		return a.AsInt() == b.AsInt()
	case (a.IsInt() || a.IsFloat()) && (b.IsInt() || b.IsFloat()):
		return numAsFloat(a) == numAsFloat(b)
	}
	return false
}

// numAsFloat returns an int or double value as a float64 for cross-type compare.
func numAsFloat(v vm.Value) float64 {
	if v.IsInt() {
		return float64(v.AsInt())
	}
	return v.AsFloat()
}

// typeNameValue is the Buzz type name of v, for assertion failure messages.
func typeNameValue(v vm.Value) string {
	switch {
	case v.IsNull():
		return "null"
	case v.IsBool():
		return "bool"
	case v.IsInt():
		return "int"
	case v.IsFloat():
		return "double"
	case v.IsStr():
		return "str"
	case v.IsList():
		return "list"
	case v.IsMap():
		return "map"
	case v.IsObject():
		return "object"
	}
	return "unknown"
}
