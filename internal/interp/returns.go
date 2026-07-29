package interp

import (
	"fmt"

	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// buzzReturnToGo converts a target's return value into a plain Go value for the
// boundary.
//
// The accepted set is deliberately tiny, and the rule is that every returnable
// type is either TERMINAL (it renders and stops) or CHAINABLE (it has CLI verbs):
//
//	void     the default; every target that exists today
//	str      terminal - `magus run version` prints 0.4.1
//	[str]    terminal - one per line as text, an array as json
//
// File and Directory join this list as the chainable pair once their mirrors
// exist; they are the reason the set is closed rather than "whatever serializes".
//
// A map is rejected on purpose despite serializing fine: it carries no verbs, so
// it can never chain, and allowing it invites targets to return arbitrary
// structures that dead-end at the CLI. A number or bool is rejected for the same
// reason - a target reporting a count returns a str, which is what the CLI prints
// either way. Objects, functions and userdata are live references to interpreter
// state and mean nothing once the session closes.
//
// There is no error return, and that is not a gap. A target signals failure
// out-of-band already (the error from the call, plus [types.WithExitCapture] for
// os.exit), so a second, ignorable failure channel would only compete with it.
func buzzReturnToGo(v vm.Value) (any, error) {
	switch {
	case v.IsNull():
		// A `> void` target, i.e. almost every target that exists. No value and no
		// error is the accurate answer, and CaptureReturn drops a nil so it never
		// reaches the rendered result as a null field.
		//nolint:nilnil // void target: no value, no error
		return nil, nil
	case v.IsStr():
		return v.AsString(), nil
	case v.IsList():
		items := v.ListItems()
		out := make([]string, 0, len(items))
		for i, it := range items {
			if !it.IsStr() {
				return nil, fmt.Errorf("a returned list must hold only str; item %d is %s", i, buzzReturnKind(it))
			}
			out = append(out, it.AsString())
		}
		return out, nil
	}
	return nil, fmt.Errorf("a target may return str, [str], or nothing; got %s", buzzReturnKind(v))
}

// buzzReturnKind names a rejected type for the errors above. It exists so the
// message says "a map" rather than a tag number, because the fix differs by kind:
// a map or object should be narrowed to the field the caller actually wants, a
// number stringified, a function not returned at all.
func buzzReturnKind(v vm.Value) string {
	switch {
	case v.IsBool():
		return "a bool"
	case v.IsInt():
		return "an int"
	case v.IsFloat():
		return "a float"
	case v.IsMap():
		return "a map"
	case v.IsList():
		return "a list"
	case v.IsFun():
		return "a function"
	case v.IsObject():
		return "an object"
	case v.IsUD():
		return "userdata"
	case v.IsType():
		return "a type"
	}
	return "an unsupported value"
}
