// Package types defines the Buzz static type system used by the type checker.
package types

import (
	"strings"
	"unicode"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
)

// Type represents a Buzz static type.
type Type interface{ TypeName() string }

// PrimitiveType is a named primitive type (int, double, str, bool, null, void, any, rng, fib).
type PrimitiveType struct{ Name string }

func (p *PrimitiveType) TypeName() string { return p.Name }

// Pre-defined primitive type singletons.
var (
	Int     Type = &PrimitiveType{"int"}
	Double  Type = &PrimitiveType{"double"}
	Str     Type = &PrimitiveType{"str"}
	Bool    Type = &PrimitiveType{"bool"}
	Null    Type = &PrimitiveType{"null"}
	Void    Type = &PrimitiveType{"void"}
	Any     Type = &PrimitiveType{"any"}       // user-written `any` annotation
	Unknown Type = &PrimitiveType{"<unknown>"} // checker tracking failure (not user-written)
	Rng     Type = &PrimitiveType{"rng"}       // range type (lo..hi)
	Fib     Type = &PrimitiveType{"fib"}       // unparameterized fiber type
	Pat     Type = &PrimitiveType{"pat"}       // pattern type ($"...")
	Ud      Type = &PrimitiveType{"ud"}        // foreign userdata (FFI opaque pointer), matching upstream's `ud`
	// TypeVal is the type OF a type used as a value: what `<[str]>` and `typeof x`
	// have. Every type value shares it, so `typeof a == typeof b` type-checks
	// regardless of which types they denote; the canonical spellings are compared
	// at runtime, not here.
	TypeVal Type = &PrimitiveType{"type"}
)

// FibType is the parameterized fiber type fib<Yield, Return>.
type FibType struct{ Yield, Return Type }

func (f *FibType) TypeName() string { return "fib" }

// ListType is the type [T]. Mut marks the `mut [T]` spelling: collections are
// immutable by default in Buzz, and mutability is part of the type rather than a
// property of the value, which is what makes `typeof list.cloneMutable()` answer
// `<mut [T]>` for the same runtime object shape.
type ListType struct {
	Elem Type
	Mut  bool
}

func (l *ListType) TypeName() string { return mutPrefix(l.Mut) + "[" + l.Elem.TypeName() + "]" }

// MapType is the type {K:V}. Mut carries the `mut {K: V}` spelling; see ListType.
//
// Tuple marks the form upstream spells `.{ a, b }`: a tuple is a map at runtime,
// keyed by each element's decimal index, but only a tuple accepts the `t.0` index
// shorthand. An anonymous object that merely happens to have a field named `@"0"`
// is not one, which is the distinction upstream's compile-error tests pin. Unlike
// Mut it is deliberately absent from TypeName: a tuple's type IS `{str:V}`, and
// rendering it differently would break annotation round-tripping through ParseAnnot.
type MapType struct {
	Key, Val Type
	Mut      bool
	Tuple    bool
}

func (m *MapType) TypeName() string {
	return mutPrefix(m.Mut) + "{" + m.Key.TypeName() + ":" + m.Val.TypeName() + "}"
}

// mutPrefix renders the `mut ` modifier, the single spelling every renderer uses
// so an annotation round-trips through ParseAnnot unchanged.
func mutPrefix(mut bool) string {
	if mut {
		return "mut "
	}
	return ""
}

// FuncType is a function type.
type FuncType struct {
	Params   []Type
	Ret      Type
	Variadic bool // if true, caller may pass any number of args beyond len(Params)
	Yield    Type // the *> yield type, when the function is wrapped in a fiber; nil if unannotated. Not part of TypeName/Compat — two functions differing only in Yield stay assignable.
	// Raises is true when the function declares a !> error set (or, for a host
	// extern, is authored as raising in std.Method). Like Yield it is not part
	// of TypeName/Compat — two functions differing only in Raises stay
	// assignable — but the checker reads it at call sites to enforce
	// propagate-or-catch: a call to a Raises function is illegal unless the
	// enclosing function also declares !> or the call is inside a try/catch.
	Raises bool
	// ParamNames carries the declared parameter names so the checker can
	// resolve named arguments at call sites. Like Yield, it is not part of
	// TypeName/Compat — names never affect assignability.
	ParamNames []string
	// ParamDefaults is parallel to ParamNames and holds each parameter's `=
	// expr` default, nil where it has none. The checker substitutes one into any
	// argument slot a call leaves empty; like ParamNames it never affects
	// assignability.
	ParamDefaults []ast.Node
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
// IsNamespace marks types built from imported module exports: they may have
// untracked fields (exported finals, vars) so unknown member access returns
// Unknown instead of an error.
type ObjectType struct {
	Name        string
	Fields      map[string]Type
	Methods     map[string]*FuncType
	IsNamespace bool
	// IsProtocol marks this as a `protocol` rather than an `object`: a set of method
	// signatures that other object types declare conformance to.
	IsProtocol bool
	// Conforms names the protocols this object type declared (`object<A, B> Name`).
	// Compat consults it, so an object is assignable to a protocol only when it said
	// so -- matching upstream, where conformance is declared and not inferred.
	Conforms []string
}

func (o *ObjectType) TypeName() string { return o.Name }

// EnumType is a named enum type.
type EnumType struct {
	Name  string
	Cases []string
	// Backing is the declared `enum<str>` / `enum<int>` backing type, "" when the
	// declaration omits it (which numbers the cases from zero, so it reads as int).
	// It is what types a case's `.value`.
	Backing string
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
	// `obj{ name: str, age: int }` is an anonymous STRUCTURAL object type. This checker
	// models named types only, so there is nothing to compare a value against: parsing
	// it to a type named "obj" made every use a mismatch (an anonymous literal infers as
	// a map). Unknown is the tracking-failure sentinel -- compatible with everything --
	// which keeps the annotation legal without asserting a shape that cannot be checked.
	if strings.HasPrefix(strings.TrimPrefix(s, "mut "), "obj{") {
		return Unknown
	}
	ap := &annotParser{s: s}
	t := ap.parse()
	if t == nil {
		return Any
	}
	return t
}

// orVoid maps an undeclared function return to Void, the type it means.
func orVoid(t Type) Type {
	if t == nil {
		return Void
	}
	return t
}

// Compat reports whether got can be assigned to want.
func Compat(got, want Type) bool {
	// A nil leaf is a legitimate representation: a function type with no declared
	// return (e.g. fun(any)) has Ret == nil, which FuncType.TypeName renders as "".
	// The structural recursion below reaches such leaves directly, so guard nil the
	// same way TypeName does: two nils match, nil vs non-nil does not.
	if got == nil || want == nil {
		return got == want
	}
	if got == Any || got == Unknown || want == Any || want == Unknown {
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
		// An omitted return type IS void in Buzz, so `fun ()` and `fun () > void`
		// name the same type even though only one of them carries a Ret leaf.
		return Compat(orVoid(gf.Ret), orVoid(wf.Ret))
	}
	// Container types: compare element-wise so the top-level Any-escape rule
	// applies inside lists and maps too — `[any]` is compatible with `[double]`,
	// just as `any` is with `double`. gopherbuzz uses Any as its dynamic-typing
	// escape hatch, so an any-element list assigns to a typed-element one.
	//
	// Mutability is DIRECTIONAL, not an equality: `mut T` is assignable to `T`,
	// and `T` is not assignable to `mut T`. Requiring the two to match rejected
	// upstream's tests/behavior/iterator.buzz, which builds a `mut [int]` and
	// returns it from a `> [int]` function - dropping the permission to mutate is
	// always safe, while gaining it is what the annotation exists to prevent
	// (upstream's tests/compile_errors/object-prop-immutable-value.buzz).
	gl, glOK := got.(*ListType)
	wl, wlOK := want.(*ListType)
	if glOK && wlOK {
		return (gl.Mut || !wl.Mut) && Compat(gl.Elem, wl.Elem)
	}
	gm, gmOK := got.(*MapType)
	wm, wmOK := want.(*MapType)
	if gmOK && wmOK {
		return (gm.Mut || !wm.Mut) && Compat(gm.Key, wm.Key) && Compat(gm.Val, wm.Val)
	}
	if got.TypeName() == want.TypeName() {
		return true
	}
	// An object is assignable to a protocol it declared conformance to. The check is
	// on the DECLARATION, not the method set: upstream rejects an object that happens
	// to have matching methods but never named the protocol, and a structural check
	// here would silently accept it. This sits AFTER the name comparison above so a
	// protocol stays compatible with itself, which has no Conforms entry of its own.
	if gotObj, gOK := got.(*ObjectType); gOK {
		if wantObj, wOK := want.(*ObjectType); wOK && wantObj.IsProtocol {
			for _, name := range gotObj.Conforms {
				if name == wantObj.Name {
					return true
				}
			}
		}
	}
	return false
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

// parse reads one type, honoring a leading `mut ` modifier. The modifier is only
// meaningful on a collection - `mut Foo` names a mutable object INSTANCE, which
// this checker models nominally and so cannot distinguish - so it is consumed and
// then applied where it has a representation.
func (p *annotParser) parse() Type {
	mut := p.acceptMut()
	t := p.parseUnmodified()
	switch v := t.(type) {
	case *ListType:
		v.Mut = mut
	case *MapType:
		v.Mut = mut
	}
	return t
}

// acceptMut consumes a leading `mut` keyword and the whitespace after it,
// reporting whether one was there. It restores the position when the identifier
// merely STARTS with "mut" (a type named `mutant`), so the modifier can never
// swallow a name.
func (p *annotParser) acceptMut() bool {
	// The leading skipSpace is unconditional (outside the restore) so a spelling
	// that separates its parts - `{str: int}`, which is how canonicalTypeName
	// renders a map - parses the same as the token-joined `{str:int}` a source
	// annotation produces.
	p.skipSpace()
	save := p.pos
	if p.readIdent() != "mut" {
		p.pos = save
		return false
	}
	// A bare `mut` with nothing after it is a type NAMED mut, not a modifier.
	if p.pos >= len(p.s) {
		p.pos = save
		return false
	}
	p.skipSpace()
	return true
}

func (p *annotParser) skipSpace() {
	for p.pos < len(p.s) && (p.s[p.pos] == ' ' || p.s[p.pos] == '\t') {
		p.pos++
	}
}

func (p *annotParser) parseUnmodified() Type {
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
