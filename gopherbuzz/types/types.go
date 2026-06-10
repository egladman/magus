// Package types defines the Buzz static type system used by the type checker.
package types

import (
	"strings"
	"unicode"
)

// Type represents a Buzz static type.
type Type interface{ TypeName() string }

// PrimitiveType is a named primitive type (int, double, str, bool, null, void, any, rng, fib).
type PrimitiveType struct{ Name string }

func (p *PrimitiveType) TypeName() string { return p.Name }

// Pre-defined primitive type singletons.
var (
	Int    Type = &PrimitiveType{"int"}
	Double Type = &PrimitiveType{"double"}
	Str    Type = &PrimitiveType{"str"}
	Bool   Type = &PrimitiveType{"bool"}
	Null   Type = &PrimitiveType{"null"}
	Void   Type = &PrimitiveType{"void"}
	Any    Type = &PrimitiveType{"any"} // unknown / unresolved
	Rng    Type = &PrimitiveType{"rng"} // range type (lo..hi)
	Fib    Type = &PrimitiveType{"fib"} // unparameterized fiber type
	Pat    Type = &PrimitiveType{"pat"} // pattern type ($"...")
	Ud     Type = &PrimitiveType{"ud"}  // foreign userdata (FFI opaque pointer), matching upstream's `ud`
)

// FibType is the parameterized fiber type fib<Yield, Return>.
type FibType struct{ Yield, Return Type }

func (f *FibType) TypeName() string { return "fib" }

// ListType is the type [T].
type ListType struct{ Elem Type }

func (l *ListType) TypeName() string { return "[" + l.Elem.TypeName() + "]" }

// MapType is the type {K:V}.
type MapType struct{ Key, Val Type }

func (m *MapType) TypeName() string {
	return "{" + m.Key.TypeName() + ":" + m.Val.TypeName() + "}"
}

// FuncType is a function type.
type FuncType struct {
	Params   []Type
	Ret      Type
	Variadic bool // if true, caller may pass any number of args beyond len(Params)
	Yield    Type // the *> yield type, when the function is wrapped in a fiber; nil if unannotated. Not part of TypeName/Compat — two functions differing only in Yield stay assignable.
	// ParamNames carries the declared parameter names so the checker can
	// resolve named arguments at call sites. Like Yield, it is not part of
	// TypeName/Compat — names never affect assignability.
	ParamNames []string
}

func (f *FuncType) TypeName() string {
	ps := make([]string, len(f.Params))
	for i, p := range f.Params {
		ps[i] = p.TypeName()
	}
	ret := ""
	if f.Ret != nil {
		ret = f.Ret.TypeName()
	}
	return "fun(" + strings.Join(ps, ",") + ")" + ret
}

// ObjectType is a named object type.
type ObjectType struct {
	Name    string
	Fields  map[string]Type
	Methods map[string]*FuncType
}

func (o *ObjectType) TypeName() string { return o.Name }

// EnumType is a named enum type.
type EnumType struct {
	Name  string
	Cases []string
}

func (e *EnumType) TypeName() string { return e.Name }

// NamedType is an unresolved reference to a user-defined type.
type NamedType struct{ Name string }

func (n *NamedType) TypeName() string { return n.Name }

// ParseAnnot parses a compact type annotation string like "int", "[str]", "fun(int)bool".
// Returns Any when the string is empty or cannot be parsed.
func ParseAnnot(s string) Type {
	if s == "" {
		return Any
	}
	ap := &annotParser{s: s}
	t := ap.parse()
	if t == nil {
		return Any
	}
	return t
}

// Compat reports whether got can be assigned to want.
func Compat(got, want Type) bool {
	if got == Any || want == Any {
		return true
	}
	if got == Null {
		return true // null is assignable to any nullable target
	}
	// Function types: compare structurally so fun(any)T is compat with fun(U)T.
	gf, gOK := got.(*FuncType)
	wf, wOK := want.(*FuncType)
	if gOK && wOK {
		if len(gf.Params) != len(wf.Params) {
			return false
		}
		for i := range gf.Params {
			if !Compat(gf.Params[i], wf.Params[i]) {
				return false
			}
		}
		return Compat(gf.Ret, wf.Ret)
	}
	return got.TypeName() == want.TypeName()
}

type annotParser struct {
	s   string
	pos int
}

func (p *annotParser) peek() byte {
	if p.pos >= len(p.s) {
		return 0
	}
	return p.s[p.pos]
}

func (p *annotParser) advance() {
	if p.peek() != 0 {
		p.pos++
	}
}

func (p *annotParser) readIdent() string {
	start := p.pos
	for p.pos < len(p.s) {
		if !isIdentContinue(rune(p.s[p.pos])) {
			break
		}
		p.pos++
	}
	return p.s[start:p.pos]
}

func (p *annotParser) skipGeneric() {
	if p.peek() != '<' {
		return
	}
	depth := 0
	for p.pos < len(p.s) {
		c := p.s[p.pos]
		p.pos++
		switch c {
		case '<':
			depth++
		case '>':
			depth--
			if depth == 0 {
				return
			}
		}
	}
}

func (p *annotParser) parse() Type {
	switch p.peek() {
	case '[':
		p.advance()
		elem := p.parse()
		if p.peek() == ']' {
			p.advance()
		}
		if p.peek() == '?' {
			p.advance()
		}
		if elem == nil {
			elem = Any
		}
		return &ListType{Elem: elem}

	case '{':
		p.advance()
		key := p.parse()
		if p.peek() == ':' {
			p.advance()
		}
		val := p.parse()
		if p.peek() == '}' {
			p.advance()
		}
		if p.peek() == '?' {
			p.advance()
		}
		if key == nil {
			key = Any
		}
		if val == nil {
			val = Any
		}
		return &MapType{Key: key, Val: val}

	default:
		if strings.HasPrefix(p.s[p.pos:], "fun") {
			p.pos += 3
			var params []Type
			if p.peek() == '(' {
				p.advance()
				for p.peek() != ')' && p.peek() != 0 {
					// A parameter may carry the upstream `name: type` spelling;
					// skip the `name:` prefix so the type alone is parsed.
					save := p.pos
					if id := p.readIdent(); id != "" && p.peek() == ':' {
						p.advance()
					} else {
						p.pos = save
					}
					pt := p.parse()
					if pt != nil {
						params = append(params, pt)
					}
					if p.peek() == ',' {
						p.advance()
					}
				}
				if p.peek() == ')' {
					p.advance()
				}
			}
			// The return type follows a `>` arrow.
			if p.peek() == '>' {
				p.advance()
			}
			var ret Type
			if p.peek() != 0 && p.peek() != '?' {
				ret = p.parse()
			}
			// A trailing `?` makes the function value itself optional.
			if p.peek() == '?' {
				p.advance()
			}
			return &FuncType{Params: params, Ret: ret}
		}

		name := p.readIdent()
		if name == "" {
			return nil
		}
		// A namespace-qualified type `ns\Type` (or `a\b\Type`) resolves by its
		// last segment — gopherbuzz binds an import's exported types unqualified
		// (the splat), so `config\Config` is the same type as `Config`. This
		// matches how a `config\Config{...}` object literal is parsed.
		for p.peek() == '\\' {
			p.advance()
			seg := p.readIdent()
			if seg == "" {
				break
			}
			name = seg
		}
		if p.peek() == '?' {
			p.advance()
		}

		switch name {
		case "int":
			return Int
		case "double":
			return Double
		case "str":
			return Str
		case "bool":
			return Bool
		case "null":
			return Null
		case "void":
			return Void
		case "any":
			return Any
		case "ud":
			// Foreign userdata (an FFI opaque pointer) — a distinct type, as in
			// upstream buzz, so a handle can't be silently used as an int/double/
			// str (those mismatches are caught here too). It bridges through
			// `any` (gopherbuzz's FFI calls are `any`-typed), which is what lets
			// the same `ud?`-threaded source check on both runtimes.
			return Ud
		case "pat":
			return Pat
		case "fib":
			if p.peek() == '<' {
				p.advance() // consume '<'
				yld := p.parse()
				if p.peek() == ',' {
					p.advance()
				}
				ret := p.parse()
				if p.peek() == '>' {
					p.advance()
				}
				if p.peek() == '?' {
					p.advance()
				}
				if yld == nil {
					yld = Any
				}
				if ret == nil {
					ret = Any
				}
				return &FibType{Yield: yld, Return: ret}
			}
			return Fib
		default:
			p.skipGeneric()
			return &NamedType{Name: name}
		}
	}
}

func isIdentContinue(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
