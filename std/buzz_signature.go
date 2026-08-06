// signature.go renders a host method's Buzz call form for docs and
// `magus describe module`. It is the hand-written companion to the generated
// trampolines (see the package doc): the Buzz surface a descriptor projects to.

package std

import (
	"strings"
)

// BuzzSignature renders a method's Buzz call form: the module imported under its
// bare name with the method camelCased, e.g. `env.lookup(name) → string, bool`.
// mod and m are the parent module and one of its methods.
// The separator is a backslash, the form upstream Buzz specifies for
// namespace member access. Docs show only that form so a reader never
// copies a notation magus is moving away from.
func BuzzSignature(mod Module, m Method) string {
	name := CamelCase(m.Name)
	if m.BuzzName != "" {
		name = m.BuzzName
	}
	return mod.Name + `\` + name + "(" + strings.Join(argNames(m), ", ") + ")" + returnSuffix(m)
}

// argNames lists the parameter names, marking variadic ones with a trailing "..."
// and bracketing optional ones. Variadic takes precedence (a "...args" already
// implies zero-or-more, so it is never also bracketed).
func argNames(m Method) []string {
	args := make([]string, 0, len(m.Args))
	for _, a := range m.Args {
		name := a.Name
		switch {
		case a.Variadic:
			name += "..."
		case a.Optional:
			name = "[" + name + "]"
		}
		args = append(args, name)
	}
	return args
}

// returnSuffix renders " → t1, t2" for a method's returns, or "" when it returns
// only the implicit error.
func returnSuffix(m Method) string {
	if len(m.Returns) == 0 {
		return ""
	}
	rets := make([]string, len(m.Returns))
	for i, r := range m.Returns {
		switch {
		case r.Name != "":
			rets[i] = r.Name
		case r.Object != "":
			// Name the object rather than the map it marshals to. "map[string]any"
			// tells a magusfile author nothing they can act on; "ExecResult" is the
			// annotation that turns field access into a checked expression, and it is
			// the only reason to know the name at all. The descriptor already carries
			// the list form ("[Commit]") when the Impl returns a slice, so this needs
			// no reflection - which matters because this package IS linked into the
			// binary, unlike the generator that fills the field in.
			rets[i] = r.Object
		default:
			rets[i] = r.Type.GoType()
		}
	}
	return " → " + strings.Join(rets, ", ")
}

// CamelCase converts a snake_case descriptor name to Buzz's camelCase (a
// single-word name is unchanged). This is the single source of truth for the
// transform: magus-utils bindings uses it to emit the Buzz map keys, and
// BuzzSignature uses it to render those same keys, so the two cannot drift.
// (TestCamelCase in std/reflect_test.go keeps an independent table on purpose, to verify them.)
func CamelCase(s string) string {
	parts := strings.Split(s, "_")
	if len(parts) == 1 {
		return s
	}
	out := parts[0]
	for _, p := range parts[1:] {
		if p != "" {
			out += strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return out
}

// BuzzMethodName is the identifier a Buzz caller types for m: the
// declared name converted to camelCase, or an explicit BuzzName override.
//
// It exists because the two names differ and only this one is callable.
// A method declared "kebab_case" is reached as "kebabCase"; documenting
// the declared form sends a reader to a symbol that does not resolve.
func BuzzMethodName(m Method) string {
	if m.BuzzName != "" {
		return m.BuzzName
	}
	return CamelCase(m.Name)
}
