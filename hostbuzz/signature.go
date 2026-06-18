// surface_signature.go renders a host method's Buzz call form for docs and
// `magus describe module`. It is the hand-written companion to the generated
// trampolines (see the package doc): the Buzz surface a descriptor projects to.

package hostbuzz

import (
	"strings"

	"github.com/egladman/magus/std"
)

// BuzzSignature renders a method's Buzz call form: the module imported under its
// bare name with the method camelCased, e.g. `env.lookup(name) → string, bool`.
// mod and m are the parent module and one of its methods.
func BuzzSignature(mod std.Module, m std.Method) string {
	return mod.Name + "." + CamelCase(m.Name) + "(" + strings.Join(argNames(m), ", ") + ")" + returnSuffix(m)
}

// argNames lists the parameter names, marking variadic ones with a trailing "..."
// and bracketing optional ones. Variadic takes precedence (a "...args" already
// implies zero-or-more, so it is never also bracketed).
func argNames(m std.Method) []string {
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
func returnSuffix(m std.Method) string {
	if len(m.Returns) == 0 {
		return ""
	}
	rets := make([]string, len(m.Returns))
	for i, r := range m.Returns {
		if r.Name != "" {
			rets[i] = r.Name
		} else {
			rets[i] = r.Type.GoType()
		}
	}
	return " → " + strings.Join(rets, ", ")
}

// CamelCase converts a snake_case descriptor name to Buzz's camelCase (a
// single-word name is unchanged). This is the single source of truth for the
// transform: magus-bindings-gen uses it to emit the Buzz map keys, and
// BuzzSignature uses it to render those same keys, so the two cannot drift.
// (hostbuzz/gen's drift test keeps an independent copy on purpose, to verify them.)
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
