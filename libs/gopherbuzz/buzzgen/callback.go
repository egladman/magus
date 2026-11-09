package buzzgen

import (
	"fmt"
	"go/ast"
	"go/types"
	"reflect"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Callbacks indexes the func types a package declares as parameters, the
// one place a callback's parameter names survive: reflect drops them.
type Callbacks struct {
	pkgPath string
	sigs    []*types.Signature
}

const callbackLoadMode = packages.NeedName | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo

// LoadCallbacks type-checks pkgPath from the module at dir and collects
// every func-typed parameter its declarations and conversions spell.
func LoadCallbacks(dir, pkgPath string) (*Callbacks, error) {
	loaded, err := packages.Load(&packages.Config{Mode: callbackLoadMode, Dir: dir}, pkgPath)
	if err != nil {
		return nil, fmt.Errorf("buzzgen: load %s: %w", pkgPath, err)
	}
	if len(loaded) != 1 || loaded[0].TypesInfo == nil {
		return nil, fmt.Errorf("buzzgen: package %s did not load", pkgPath)
	}
	pkg := loaded[0]
	if len(pkg.Errors) > 0 {
		return nil, fmt.Errorf("buzzgen: %s: %v", pkgPath, pkg.Errors[0])
	}
	return collectCallbacks(pkgPath, pkg.Syntax, pkg.TypesInfo), nil
}

func collectCallbacks(pkgPath string, files []*ast.File, info *types.Info) *Callbacks {
	c := &Callbacks{pkgPath: pkgPath}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncType)
			if !ok || fn.Params == nil {
				return true
			}
			for _, field := range fn.Params.List {
				if _, isFunc := field.Type.(*ast.FuncType); !isFunc {
					continue
				}
				if sig, ok := info.TypeOf(field.Type).(*types.Signature); ok {
					c.sigs = append(c.sigs, sig)
				}
			}
			return true
		})
	}
	return c
}

// Params returns the names of callback t's parameters after its leading
// context.Context, which become the Buzz labels. Every declaration of t's
// shape must name them, and name them alike.
func (c *Callbacks) Params(t reflect.Type) ([]string, error) {
	var names []string
	found := false
	for _, sig := range c.sigs {
		if !sameType(sig, t) {
			continue
		}
		got := make([]string, 0, sig.Params().Len())
		for i := 1; i < sig.Params().Len(); i++ {
			name := sig.Params().At(i).Name()
			if name == "" || name == "_" {
				return nil, fmt.Errorf("buzzgen: callback %s in %s leaves parameter %d unnamed, and its name is the Buzz label", t, c.pkgPath, i)
			}
			got = append(got, name)
		}
		if found && !slices.Equal(names, got) {
			return nil, fmt.Errorf("buzzgen: callback %s is declared in %s with parameters (%s) and (%s); name them alike", t, c.pkgPath, strings.Join(names, ", "), strings.Join(got, ", "))
		}
		names, found = got, true
	}
	if !found {
		return nil, fmt.Errorf("buzzgen: no parameter of %s declares callback %s", c.pkgPath, t)
	}
	return names, nil
}

// FuncType renders callback t as a Buzz function type whose parameters
// after the context carry names as labels, such as
// `fun (path: str) > bool !> any`.
func FuncType(t reflect.Type, names []string, opts Options) (string, error) {
	if t.NumIn()-1 != len(names) {
		return "", fmt.Errorf("buzzgen: callback %s takes %d parameters after its context, given %d names", t, t.NumIn()-1, len(names))
	}
	params := make([]string, len(names))
	for i, name := range names {
		typ, err := callbackValueType(t.In(i+1), opts)
		if err != nil {
			return "", fmt.Errorf("buzzgen: callback %s parameter %s: %w", t, name, err)
		}
		params[i] = name + ": " + typ
	}
	ret := "void"
	if t.NumOut() == 2 {
		typ, err := callbackValueType(t.Out(0), opts)
		if err != nil {
			return "", fmt.Errorf("buzzgen: callback %s result: %w", t, err)
		}
		ret = typ
	}
	return fmt.Sprintf("fun (%s) > %s !> any", strings.Join(params, ", "), ret), nil
}

// callbackValueType is FieldType, except an interface is `any`: a field
// may be absent, but a callback's value is whatever the function returned.
func callbackValueType(t reflect.Type, opts Options) (string, error) {
	if t.Kind() == reflect.Interface {
		return "any", nil
	}
	typ, _, err := FieldType(t, opts)
	return typ, err
}

// sameType reports whether go/types type g and reflect type r name the same
// Go type. Named types compare by package path and name, so an alias in
// either spelling matches.
func sameType(g types.Type, r reflect.Type) bool {
	g = types.Unalias(g)
	if named, ok := g.(*types.Named); ok {
		obj := named.Obj()
		if obj.Pkg() == nil {
			return r.PkgPath() == "" && r.Name() == obj.Name()
		}
		return r.PkgPath() == obj.Pkg().Path() && r.Name() == obj.Name()
	}
	switch g := g.(type) {
	case *types.Basic:
		return r.PkgPath() == "" && r.Name() != "" && types.Typ[g.Kind()].Name() == r.Kind().String()
	case *types.Slice:
		return r.Kind() == reflect.Slice && sameType(g.Elem(), r.Elem())
	case *types.Array:
		return r.Kind() == reflect.Array && int64(r.Len()) == g.Len() && sameType(g.Elem(), r.Elem())
	case *types.Pointer:
		return r.Kind() == reflect.Pointer && sameType(g.Elem(), r.Elem())
	case *types.Map:
		return r.Kind() == reflect.Map && sameType(g.Key(), r.Key()) && sameType(g.Elem(), r.Elem())
	case *types.Interface:
		return r.Kind() == reflect.Interface && r.Name() == "" && g.NumMethods() == r.NumMethod()
	case *types.Signature:
		return sameSignature(g, r)
	}
	return false
}

func sameSignature(g *types.Signature, r reflect.Type) bool {
	if r.Kind() != reflect.Func || g.Variadic() != r.IsVariadic() ||
		g.Params().Len() != r.NumIn() || g.Results().Len() != r.NumOut() {
		return false
	}
	for i := range r.NumIn() {
		if !sameType(g.Params().At(i).Type(), r.In(i)) {
			return false
		}
	}
	for i := range r.NumOut() {
		if !sameType(g.Results().At(i).Type(), r.Out(i)) {
			return false
		}
	}
	return true
}
