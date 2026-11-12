package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
	"github.com/egladman/magus/std"
)

// stdCallbacks indexes the callback types std declares. It loads once, on
// first use, because only the bindings and declarations read it.
var stdCallbacks = sync.OnceValue(func() *buzzgen.Callbacks {
	root, err := buzzgenRoot()
	if err == nil {
		var cbs *buzzgen.Callbacks
		cbs, err = buzzgen.LoadCallbacks(root, reflect.TypeFor[std.Module]().PkgPath())
		if err == nil {
			return cbs
		}
	}
	fmt.Fprintln(os.Stderr, "magus-utils:", err)
	os.Exit(1)
	return nil
})

// callbackBuzzType is the Buzz function type a callback parameter of Go
// type t declares.
func callbackBuzzType(t reflect.Type) (string, error) {
	names, err := stdCallbacks().Params(t)
	if err != nil {
		return "", err
	}
	return buzzgen.FuncType(t, names, mirrorOptions())
}

// callbackAdapterName names the generated adapter for callback type t.
func callbackAdapterName(t reflect.Type) string {
	return emit.CallbackFuncName(t, typeIdent)
}

// implementationCallbacks returns every distinct callback type an Impl in mods
// takes, sorted by adapter name. Two types sharing a name is an error.
func implementationCallbacks(mods []std.Module) ([]reflect.Type, error) {
	byName := map[string]reflect.Type{}
	for _, m := range mods {
		for _, meth := range slices.Concat(m.Methods, m.ObjectExterns()) {
			if meth.Impl == nil {
				continue
			}
			sig := reflect.TypeOf(meth.Impl)
			for i := 1; i < sig.NumIn(); i++ {
				t := sig.In(i)
				if t.Kind() != reflect.Func {
					continue
				}
				name := callbackAdapterName(t)
				if prior, ok := byName[name]; ok && prior != t {
					return nil, fmt.Errorf("callbacks %s and %s both name adapter %s", prior, t, name)
				}
				byName[name] = t
			}
		}
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for name := range byName {
			if !yield(name) {
				return
			}
		}
	})
	out := make([]reflect.Type, len(names))
	for i, name := range names {
		out[i] = byName[name]
	}
	return out, nil
}

// emitCallbacks renders the adapters file: one function per callback type,
// turning a Buzz function value into that Go func.
func emitCallbacks(cbs []reflect.Type, pkg string) ([]byte, error) {
	pkgs := &argPackages{}
	var body bytes.Buffer
	for _, t := range cbs {
		if err := emitCallbackAdapter(&body, pkgs, t); err != nil {
			return nil, err
		}
	}
	imports := []emit.GoImport{
		{Alias: "buzz", Path: "github.com/egladman/magus/libs/gopherbuzz"},
		{Alias: "vm", Path: "github.com/egladman/magus/libs/gopherbuzz/vm"},
		{Path: "github.com/egladman/magus/internal/interp/bindings/ffi"},
	}
	for _, p := range pkgs.packages() {
		imports = append(imports, emit.GoImport{Alias: packageAlias(p), Path: p})
	}
	b := bytes.NewBuffer(emit.GoHeader("magus-utils bindings", pkg, []emit.GoImport{{Path: "context"}}, imports))
	b.Write(body.Bytes())
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, gofmtError(err, b.String())
	}
	return out, nil
}

func emitCallbackAdapter(w *bytes.Buffer, pkgs *argPackages, t reflect.Type) error {
	names, err := stdCallbacks().Params(t)
	if err != nil {
		return err
	}
	params := make([]string, len(names))
	values := make([]string, len(names))
	for i, name := range names {
		pt := t.In(i + 1)
		value, err := callbackArgValue(pt, name)
		if err != nil {
			return fmt.Errorf("callback %s parameter %s: %w", t, name, err)
		}
		values[i] = value
		params[i] = fmt.Sprintf("%s %s", name, spellType(pkgs, pt))
	}
	results := "error"
	if t.NumOut() == 2 {
		results = fmt.Sprintf("(%s, error)", spellType(pkgs, t.Out(0)))
	}
	goType := fmt.Sprintf("func(ctx context.Context%s) %s", prefixed(params), results)
	name := callbackAdapterName(t)
	fmt.Fprintf(w, "// %s adapts a Buzz function value as a Go func.\n", name)
	fmt.Fprintf(w, "func %s(sess *buzz.Session, args []vm.Value, n int) %s {\n", name, goType)
	fmt.Fprintln(w, "\tfn, ok := ffi.Fun(args, n)")
	fmt.Fprintln(w, "\tif !ok {")
	fmt.Fprintln(w, "\t\treturn nil")
	fmt.Fprintln(w, "\t}")
	fmt.Fprintf(w, "\treturn %s {\n", goType)
	call := fmt.Sprintf("sess.CallValue(ctx, fn, []vm.Value{%s})", strings.Join(values, ", "))
	if t.NumOut() == 1 {
		fmt.Fprintf(w, "\t\t_, err := %s\n", call)
		fmt.Fprintln(w, "\t\treturn err")
		fmt.Fprintln(w, "\t}")
		fmt.Fprintln(w, "}")
		fmt.Fprintln(w)
		return nil
	}
	rt := t.Out(0)
	fmt.Fprintf(w, "\t\tres, err := %s\n", call)
	fmt.Fprintln(w, "\t\tif err != nil {")
	fmt.Fprintf(w, "\t\t\tvar zero %s\n", spellType(pkgs, rt))
	fmt.Fprintln(w, "\t\t\treturn zero, err")
	fmt.Fprintln(w, "\t\t}")
	if err := emitCallbackResult(w, pkgs, rt); err != nil {
		return fmt.Errorf("callback %s result: %w", t, err)
	}
	fmt.Fprintln(w, "\t}")
	fmt.Fprintln(w, "}")
	fmt.Fprintln(w)
	return nil
}

// emitCallbackResult emits the decode of res as rt and the return of it.
func emitCallbackResult(w *bytes.Buffer, pkgs *argPackages, rt reflect.Type) error {
	if e, ok := buzzEnum(rt); ok {
		values := make([]string, len(e.Cases))
		for i, c := range e.Cases {
			values[i] = c.Value
		}
		fmt.Fprintf(w, "\t\ts, err := ffi.ResultEnum(res, %q, %#v)\n", e.Name, values)
		fmt.Fprintf(w, "\t\treturn %s(s), err\n", spellType(pkgs, rt))
		return nil
	}
	switch tag := std.GoTypeTag(rt); {
	case tag == std.TypeBool && rt.PkgPath() == "":
		fmt.Fprintln(w, "\t\treturn ffi.ResultBool(res)")
	case tag == std.TypeAny && rt.NumMethod() == 0:
		fmt.Fprintln(w, "\t\treturn ffi.ResultAny(res), nil")
	default:
		return fmt.Errorf("no adapter decodes a %s", rt)
	}
	return nil
}

// callbackArgValue is the expression encoding Go value src of type t for
// the Buzz function.
func callbackArgValue(t reflect.Type, src string) (string, error) {
	if t.Kind() == reflect.String && t.PkgPath() != "" {
		return fmt.Sprintf("ffi.StringVal(string(%s))", src), nil
	}
	if t.PkgPath() != "" || t.Kind() == reflect.Struct {
		return "", fmt.Errorf("no adapter encodes a %s", t)
	}
	tag := std.GoTypeTag(t)
	if tag == std.TypeInvalid {
		return "", fmt.Errorf("no adapter encodes a %s", t)
	}
	return buzzValConv(tag, src), nil
}

// spellType is t as the adapters file writes it, recording its package.
func spellType(pkgs *argPackages, t reflect.Type) string {
	if t.Kind() == reflect.Interface && t.Name() == "" && t.NumMethod() == 0 {
		return "any"
	}
	if t.PkgPath() == "" {
		return t.String()
	}
	pkgs.spell(t)
	return qualifyType(t)
}

func prefixed(params []string) string {
	if len(params) == 0 {
		return ""
	}
	return ", " + strings.Join(params, ", ")
}
