package std

import "strings"

// TealSignature renders a method's Teal/Lua call form: the module bound by
// require, snake_case method, e.g. `env.lookup(name) → string, bool`. mod and m
// are the parent module and one of its methods.
func TealSignature(mod Module, m Method) string {
	return mod.Name + "." + m.Name + "(" + strings.Join(argNames(m), ", ") + ")" + returnSuffix(m)
}

// BuzzSignature renders a method's Buzz call form: reached off the `extra`
// aggregate with the name camelCased, e.g. `extra.env.lookup(name) → string, bool`.
// mod and m are the parent module and one of its methods.
func BuzzSignature(mod Module, m Method) string {
	return "extra." + mod.Name + "." + CamelCase(m.Name) + "(" + strings.Join(argNames(m), ", ") + ")" + returnSuffix(m)
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
// (gen/buzz's drift test keeps an independent copy on purpose, to verify them.)
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
