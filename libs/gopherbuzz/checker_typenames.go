package buzz

import (
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/types"
)

// builtinTypeWords are the words an annotation spells without naming a declared
// type. rg, type and obj reach the checker as a NamedType; the rest never do, and
// are listed so an annotation made only of them skips the parse.
var builtinTypeWords = map[string]bool{
	"int": true, "double": true, "str": true, "bool": true, "null": true, "void": true,
	"any": true, "ud": true, "pat": true, "fib": true, "fun": true, "mut": true,
	"rg": true, "rng": true, "type": true, "obj": true,
}

// checkTypeNames reports a declaration annotation naming a type nothing declares,
// as upstream reports a placeholder still unresolved at the end of a parse
// (Parser.zig resolveGlobal). Unreported, the name survives as a NamedType, which
// every later check reads as an erased type parameter, so a parameter typed by it
// accepts any argument.
func (c *checker) checkTypeNames(prog *ast.Program) {
	w := typeNameWalk{c: c}
	for _, s := range prog.Stmts {
		w.walk(s, nil)
	}
}

type typeNameWalk struct {
	c *checker
	// reported keeps one error per spelling at one position: a function reports
	// every annotation at its own position.
	reported map[typeNameReport]bool
}

type typeNameReport struct {
	pos     ast.Pos
	spelled string
}

func (w *typeNameWalk) walk(root ast.Node, generics map[string]bool) {
	ast.Inspect(root, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FunDecl:
			w.fun(v.Pos, withTypeParams(generics, v.TypeParams), v.ParamAnnots, v.RetAnnot, v.YieldAnnot, v.ParamDefaults, v.Body)
			return false
		case *ast.FunExpr:
			w.fun(v.Pos, withTypeParams(generics, v.TypeParams), v.ParamAnnots, v.RetAnnot, v.YieldAnnot, v.ParamDefaults, v.Body)
			return false
		case *ast.ObjectDecl:
			inner := withTypeParams(generics, v.TypeParams)
			for _, fields := range [][]ast.ObjField{v.Fields, v.StaticFields} {
				for _, f := range fields {
					w.annot(v.Pos, f.TypeAnnot, inner)
					w.walk(f.Default, inner)
				}
			}
			for _, m := range v.Methods {
				w.walk(m, inner)
			}
			return false
		case *ast.DeclStmt:
			w.annot(v.Pos, v.TypeAnnot, generics)
		}
		return true
	})
}

func (w *typeNameWalk) fun(pos ast.Pos, generics map[string]bool, params []string, ret, yield string, defaults []ast.Node, body *ast.BlockStmt) {
	for _, a := range params {
		w.annot(pos, a, generics)
	}
	w.annot(pos, ret, generics)
	w.annot(pos, yield, generics)
	for _, d := range defaults {
		w.walk(d, generics)
	}
	if body != nil {
		w.walk(body, generics)
	}
}

func (w *typeNameWalk) annot(pos ast.Pos, annot string, generics map[string]bool) {
	if !namesAType(annot) {
		return
	}
	generics = withTypeParams(generics, funTypeParams(annot))
	quals := qualifiedTypeNames(annot)
	eachNamedType(types.ParseAnnot(annot), func(name string) {
		if generics[name] || builtinTypeWords[name] {
			return
		}
		qual := quals[name]
		spelled := name
		if qual != "" {
			spelled = qual + `\` + name
		}
		key := typeNameReport{pos, spelled}
		if w.reported[key] {
			return
		}
		if msg := w.c.undefinedTypeName(qual, name); msg != "" {
			if w.reported == nil {
				w.reported = map[typeNameReport]bool{}
			}
			w.reported[key] = true
			w.c.errorfc(pos, UndefinedType, "%s", msg)
		}
	})
}

// undefinedTypeName is the message for a type name that resolves to nothing, or ""
// when it resolves. A qualifier bound to a namespace the checker built must declare
// the type itself; any other qualifier (a module declaring nothing, a `namespace`
// line reached through a flat import) falls back to the bare name.
func (c *checker) undefinedTypeName(qual, name string) string {
	if qual != "" {
		if t, known := c.namespaceMemberType(qual, name); known {
			if t != nil {
				return ""
			}
			return `undefined type "` + qual + `\` + name + `": ` + qual + " declares no type " + name
		}
	}
	if _, ok := c.namedType(name); ok {
		return ""
	}
	// A type an earlier chunk of this session declared reaches this one as a
	// global only, bound as Unknown.
	if e, ok := c.lookup(name); ok {
		switch e.typ.(type) {
		case *types.ObjectType, *types.EnumType:
			return ""
		}
		if e.typ == types.Unknown {
			return ""
		}
	}
	if qual != "" {
		name = qual + `\` + name
	}
	return `undefined type "` + name + `"`
}

// namespaceMemberType resolves `qual\name` through the namespace bound to qual.
// known is false when qual is not a namespace the checker built; t is nil when it
// is one that declares no type name.
func (c *checker) namespaceMemberType(qual, name string) (t types.Type, known bool) {
	if strings.IndexByte(qual, '\\') >= 0 {
		return nil, false
	}
	e, ok := c.lookup(qual)
	if !ok {
		return nil, false
	}
	ns, isNS := e.typ.(*types.ObjectType)
	if !isNS || !ns.IsNamespace {
		return nil, false
	}
	switch member := ns.Fields[name].(type) {
	case *types.ObjectType, *types.EnumType:
		return member, true
	}
	return nil, true
}

// namesAType reports whether annot spells any word that is not a builtin type,
// which is the only way it can name a declared one.
func namesAType(annot string) bool {
	for i := 0; i < len(annot); {
		if !isTypeIdentStart(annot[i]) {
			i++
			continue
		}
		start := i
		for i < len(annot) && isTypeIdentByte(annot[i]) {
			i++
		}
		if !builtinTypeWords[annot[start:i]] {
			return true
		}
	}
	return false
}

// qualifiedTypeNames maps the last segment of each `ns\T` in annot to its
// qualifier. types.ParseAnnot keeps only the last segment, so the qualifier is
// read from the text.
func qualifiedTypeNames(annot string) map[string]string {
	if strings.IndexByte(annot, '\\') < 0 {
		return nil
	}
	var quals map[string]string
	for i := 0; i < len(annot); {
		if !isTypeIdentStart(annot[i]) {
			i++
			continue
		}
		start, last := i, i
		for i < len(annot) && isTypeIdentByte(annot[i]) {
			i++
		}
		for i+1 < len(annot) && annot[i] == '\\' && isTypeIdentStart(annot[i+1]) {
			i++
			last = i
			for i < len(annot) && isTypeIdentByte(annot[i]) {
				i++
			}
		}
		if last > start {
			if quals == nil {
				quals = map[string]string{}
			}
			quals[annot[last:i]] = annot[start : last-1]
		}
	}
	return quals
}

// funTypeParams returns the type parameters a function TYPE in annot declares
// (`fun::<C, D>() > int`); the parser keeps that clause in the annotation text.
func funTypeParams(annot string) []string {
	var names []string
	for rest := annot; ; {
		i := strings.Index(rest, "::<")
		if i < 0 {
			return names
		}
		rest = rest[i+3:]
		end := strings.IndexByte(rest, '>')
		if end < 0 {
			return names
		}
		for _, n := range strings.Split(rest[:end], ",") {
			names = append(names, strings.TrimSpace(n))
		}
		rest = rest[end+1:]
	}
}

func eachNamedType(t types.Type, fn func(string)) {
	switch v := t.(type) {
	case *types.NamedType:
		fn(v.Name)
	case *types.ListType:
		eachNamedType(v.Elem, fn)
	case *types.MapType:
		eachNamedType(v.Key, fn)
		eachNamedType(v.Val, fn)
	case *types.FibType:
		eachNamedType(v.Yield, fn)
		eachNamedType(v.Return, fn)
	case *types.FuncType:
		for _, p := range v.Params {
			eachNamedType(p, fn)
		}
		eachNamedType(v.Ret, fn)
	}
}

func withTypeParams(outer map[string]bool, names []string) map[string]bool {
	if len(names) == 0 {
		return outer
	}
	inner := make(map[string]bool, len(outer)+len(names))
	for n := range outer {
		inner[n] = true
	}
	for _, n := range names {
		inner[n] = true
	}
	return inner
}

func isTypeIdentStart(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isTypeIdentByte(b byte) bool {
	return isTypeIdentStart(b) || (b >= '0' && b <= '9')
}
