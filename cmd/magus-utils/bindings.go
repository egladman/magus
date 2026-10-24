// Subcommand `bindings` emits per-VM trampoline code from std.Module
// declarations. The generated file registers the module's methods on a backend
// Session: decode args, call the Impl in the host package, encode results.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
	"go/format"
	"path"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/internal/hostmodules"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

func runBindings(args []string) error {
	fs := flag.NewFlagSet("bindings", flag.ExitOnError)
	moduleName := fs.String("module", "", "module name to generate (e.g. fs)")
	lang := fs.String("lang", "buzz", "codegen target: buzz")
	outPath := fs.String("out", "", "output file path")
	pkg := fs.String("pkg", "", "package clause of the generated file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *moduleName == "" || *outPath == "" || *pkg == "" {
		return fmt.Errorf("usage: magus-utils bindings -module <name> -lang buzz -out <path> -pkg <package>")
	}

	m, ok := hostmodules.Get(*moduleName)
	if !ok {
		return fmt.Errorf("unknown module %q", *moduleName)
	}

	if *lang != "buzz" {
		return fmt.Errorf("unknown lang %q (have: buzz)", *lang)
	}

	// Checked across EVERY module, not just the one being generated: the
	// declarations are one table, and a per-module check would report a mismatch
	// only when that module happened to be the regenerate target.
	if err := checkObjectDecls(hostmodules.All()); err != nil {
		return err
	}
	out, err := emitBuzz(m, *pkg)
	if err != nil {
		return fmt.Errorf("render buzz declarations: %w", err)
	}
	if err := emit.File(*outPath, out); err != nil {
		return fmt.Errorf("write %s: %w", *outPath, err)
	}
	return nil
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	if s[0] >= 'a' && s[0] <= 'z' {
		return string(s[0]-'a'+'A') + s[1:]
	}
	return s
}

func registerName(module string) string {
	return "Register" + titleCase(module)
}

func goLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "nil"
	case string:
		return fmt.Sprintf("%q", x)
	case int:
		return fmt.Sprintf("%d", x)
	case int64:
		return fmt.Sprintf("%d", x)
	case float64:
		return fmt.Sprintf("%g", x)
	case bool:
		if x {
			return "true"
		}
		return "false"
	}
	return fmt.Sprintf("%#v", v)
}

// emitBuzz emits the Buzz trampolines for module m into package pkg
// (bindgen, in internal/interp/bindings/gen):
// one Register<Module>(ctx, sess) that builds a buzz map of DirectValue closures.
// Buzz closures return (Value, error) natively, so host errors propagate as a Buzz
// runtime error instead of a panic. Method names stay snake_case to match the
// magusfile DSL; multi-return Impls yield a list.
//
// It emits directly against the concrete magus/gopherbuzz value system. Typed
// object returns get a concrete, generated encoder in this package: no reflection
// and no map[string]any intermediary are linked into the magus runtime path.
func emitBuzz(m std.Module, pkg string) ([]byte, error) {
	// Every Method's Impl (and Field's Resolver) has to live in the SAME Go
	// package: one generated file makes one Register<Module> call, importing
	// one Impl package under one qualifier. That was always true when every
	// Impl lived in std; std/encoding's leaf packages don't change it, they
	// just mean the qualifier is no longer always "std".
	implPath, implPkg, err := resolveImplPackage(m)
	if err != nil {
		return nil, err
	}

	// Build the func body first, then derive the import set from what it uses:
	// the Impl package (Impls/resolvers) appears only when the body references
	// it, so an unused import can't sneak in.
	var body bytes.Buffer

	regFn := registerName(m.Name)
	fmt.Fprintf(&body, "// %s builds the %q module map and returns it.\n", regFn, m.Name)
	if m.Doc != "" {
		// Every line prefixed, not just the first: a module Doc may be several
		// paragraphs (the magus module's names the provider namespaces it cannot
		// declare), and emitting an embedded newline raw put prose outside the comment
		// and failed gofmt with "expected declaration, found <first word of line 2>".
		for _, line := range strings.Split(m.Doc, "\n") {
			if line == "" {
				fmt.Fprintln(&body, "//")
				continue
			}
			fmt.Fprintf(&body, "// %s\n", line)
		}
	}
	fmt.Fprintf(&body, "func %s(ctx context.Context, sess *buzz.Session) vm.Value {\n", regFn)
	fmt.Fprintln(&body, "\t_ = ctx")
	fmt.Fprintln(&body, "\t_ = sess")
	fmt.Fprintln(&body, "\tm := vm.NewMap()")

	// Discarded: this emitter is here to NAME the shared encoders and to reject an
	// undeclared return, not to write one. The bodies are emitted once for the whole
	// package by `magus-utils boundaryobjects`.
	objects := newBuzzValueEmitter()
	decoders := &optionDecoders{}
	var members bytes.Buffer
	for _, f := range m.Fields {
		emitBuzzField(&members, f, implPkg)
	}
	for _, meth := range m.Methods {
		// An Extern member has no Impl to wrap; internal/interp/bindings MapSets it
		// onto the namespace at run time. It is declared, not trampolined.
		if meth.Extern {
			continue
		}
		if err := emitBuzzMethod(&members, m, meth, objects, decoders, implPkg); err != nil {
			return nil, err
		}
	}
	// One resolver per registration, which is one per session: the cases it hands out
	// are the ones this session's enum definitions interned.
	if objects.usesEnums {
		importPath := m.Path
		if importPath == "" {
			importPath = m.Name
		}
		fmt.Fprintf(&body, "\tenums := ffi.SessionEnums(ctx, sess, %q)\n", importPath)
	}
	body.Write(members.Bytes())

	fmt.Fprintln(&body, "\treturn m")
	fmt.Fprintln(&body, "}")
	decoders.emit(&body)

	var b bytes.Buffer
	// An IO module calls std functions that are themselves excluded from the wasm
	// build, so its trampoline cannot compile there either; tag it off wasm to match.
	//
	// Derived from std.Module.WASM rather than a list kept here. That list existed,
	// carried "Keep in sync with the //go:build !wasm tags", and had drifted: `proc`
	// was missing from it, so gen/proc.go was generated with NO build tag and broke
	// the wasm build by referencing std.OsExec, which does not exist there. A
	// classification kept in three hand-maintained places (this map and the two
	// Modules tables) is one nobody can keep true. It now has one source: std
	// declares it, this tag and both tables are generated from it, and everything
	// downstream (dry.WASMCompatibleMagusModules, and the docs generator's
	// runs-in-the-browser marker through it) was already derived.
	if !m.WASM {
		fmt.Fprintln(&b, "//go:build !wasm")
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b, "// Code generated by magus-utils bindings. DO NOT EDIT.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "package "+pkg)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, `import (`)
	fmt.Fprintln(&b, `	"context"`)
	if len(decoders.types) > 0 {
		fmt.Fprintln(&b, `	"fmt"`)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, `	buzz "github.com/egladman/magus/libs/gopherbuzz"`)
	fmt.Fprintln(&b, `	vm "github.com/egladman/magus/libs/gopherbuzz/vm"`)
	if bytes.Contains(body.Bytes(), []byte("ffi.")) {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/internal/interp/bindings/ffi"`)
	}
	if implPath != "" {
		fmt.Fprintf(&b, "\t%q\n", implPath)
	}
	for _, p := range decoders.packages() {
		if p != implPath {
			fmt.Fprintf(&b, "\t%q\n", p)
		}
	}
	fmt.Fprintln(&b, `)`)
	fmt.Fprintln(&b)
	b.Write(body.Bytes())

	out, err := format.Source(b.Bytes())
	if err != nil {
		// Return nil on failure: the unformatted buffer is known-bad Go.
		return nil, gofmtError(err, b.String())
	}
	return out, nil
}

// resolveImplPackage returns the Go import path and local package identifier every
// one of m's Method.Impl and Field.Resolver funcs share; "share" because one
// generated file makes one Register<Module> call under one import, so a
// module whose Impls span two packages cannot be emitted as written. Errors
// if m declares no Impl/Resolver at all (nothing to import) or if they
// disagree on package.
func resolveImplPackage(m std.Module) (importPath, pkgIdent string, err error) {
	check := func(path, pkg, where string) error {
		if importPath == "" && pkgIdent == "" {
			importPath, pkgIdent = path, pkg
			return nil
		}
		if path != importPath {
			return fmt.Errorf("%s: Impl/Resolver package %q disagrees with %q used elsewhere in this module, a generated file can only import one Impl package", where, path, importPath)
		}
		return nil
	}
	for _, f := range m.Fields {
		path, pkg := std.FieldResolverPackage(f)
		if path == "" {
			return "", "", fmt.Errorf("field %q: Resolver is nil or not a function", f.Name)
		}
		if err := check(path, pkg, "field "+f.Name); err != nil {
			return "", "", err
		}
	}
	for _, meth := range m.Methods {
		if meth.Extern {
			continue // no Impl, so no package to import
		}
		path, pkg := std.MethodImplPackage(meth)
		if path == "" {
			return "", "", fmt.Errorf("method %q: Impl is nil or not a function", meth.Name)
		}
		if err := check(path, pkg, "method "+meth.Name); err != nil {
			return "", "", err
		}
	}
	return importPath, pkgIdent, nil
}

// emitBuzzField resolves a static field once and stores it on the module map.
// The key is camelCased (Buzz convention) though the std descriptor is snake_case.
func emitBuzzField(w *bytes.Buffer, f std.Field, implPkg string) {
	callArgs := ""
	if std.FieldResolverTakesCtx(f) {
		callArgs = "ctx"
	}
	fmt.Fprintf(w, "\tif v, err := %s.%s(%s); err == nil {\n", implPkg, std.FieldFuncName(f), callArgs)
	fmt.Fprintf(w, "\t\tm.MapSet(%q, %s)\n", std.CamelCase(f.Name), buzzValConv(f.Type, "v"))
	fmt.Fprintln(w, "\t}")
}

// emitBuzzMethod emits one DirectValue trampoline for meth. The closure's arg
// slice is named bzArgs to avoid colliding with host args named "args". The map
// key is camelCased to match Buzz's convention (the snake_case descriptor name
// stays the runtime label so errors still read e.g. "fs.readFile"). implPkg is
// the local identifier meth.Impl's package is imported under: "std" for a
// module still living in std's flat root, or e.g. "json" for one implemented
// in a std/encoding leaf package (see resolveImplPackage).
func emitBuzzMethod(w *bytes.Buffer, m std.Module, meth std.Method, objects *buzzValueEmitter, decoders *optionDecoders, implPkg string) error {
	name := std.CamelCase(meth.Name)
	if meth.BuzzName != "" {
		name = meth.BuzzName
	}
	fmt.Fprintf(w, "\tm.MapSet(%q, vm.DirectValue(%q, func(ctx context.Context, bzArgs []vm.Value) (vm.Value, error) {\n",
		name, fmt.Sprintf("%s.%s", m.Name, name))

	impl := reflect.TypeOf(meth.Impl)
	for i, a := range meth.Args {
		if a.Type == std.TypeAnyMap && !a.Variadic && impl.In(i+1).Kind() == reflect.Struct {
			fn, err := decoders.add(impl.In(i + 1))
			if err != nil {
				return fmt.Errorf("%s\\%s: %w", m.Name, meth.Name, err)
			}
			fmt.Fprintf(w, "\t\t%s, err := %s(%q, ffi.AnyMap(bzArgs, %d))\n", a.Name, fn, fmt.Sprintf("%s\\%s", m.Name, name), i)
			fmt.Fprintln(w, "\t\tif err != nil {")
			fmt.Fprintln(w, "\t\t\treturn vm.Null, ffi.Error(err)")
			fmt.Fprintln(w, "\t\t}")
			continue
		}
		emitBuzzArgDecode(w, a, i)
	}

	callArgs := make([]string, 0, 1+len(meth.Args))
	callArgs = append(callArgs, "ctx")
	for _, a := range meth.Args {
		if a.Variadic {
			callArgs = append(callArgs, a.Name+"...")
		} else {
			callArgs = append(callArgs, a.Name)
		}
	}
	callStr := strings.Join(callArgs, ", ")

	switch len(meth.Returns) {
	case 0:
		fmt.Fprintf(w, "\t\tif err := %s.%s(%s); err != nil {\n", implPkg, std.MethodFuncName(meth), callStr)
		fmt.Fprintln(w, "\t\t\treturn vm.Null, ffi.Error(err)")
		fmt.Fprintln(w, "\t\t}")
		fmt.Fprintln(w, "\t\treturn vm.Null, nil")
	case 1:
		fmt.Fprintf(w, "\t\tret0, err := %s.%s(%s)\n", implPkg, std.MethodFuncName(meth), callStr)
		fmt.Fprintln(w, "\t\tif err != nil {")
		fmt.Fprintln(w, "\t\t\treturn vm.Null, ffi.Error(err)")
		fmt.Fprintln(w, "\t\t}")
		value, err := returnConv(meth.Returns[0], reflect.TypeOf(meth.Impl).Out(0), "ret0", objects)
		if err != nil {
			return fmt.Errorf("%s\\%s: %w", m.Name, meth.Name, err)
		}
		fmt.Fprintf(w, "\t\treturn %s, nil\n", value)
	default:
		lhsParts := make([]string, 0, len(meth.Returns)+1)
		for i := range meth.Returns {
			lhsParts = append(lhsParts, fmt.Sprintf("ret%d", i))
		}
		lhsParts = append(lhsParts, "err")
		fmt.Fprintf(w, "\t\t%s := %s.%s(%s)\n", strings.Join(lhsParts, ", "), implPkg, std.MethodFuncName(meth), callStr)
		fmt.Fprintln(w, "\t\tif err != nil {")
		fmt.Fprintln(w, "\t\t\treturn vm.Null, ffi.Error(err)")
		fmt.Fprintln(w, "\t\t}")
		items := make([]string, len(meth.Returns))
		for i, ret := range meth.Returns {
			value, err := returnConv(ret, reflect.TypeOf(meth.Impl).Out(i), fmt.Sprintf("ret%d", i), objects)
			if err != nil {
				return fmt.Errorf("%s\\%s: %w", m.Name, meth.Name, err)
			}
			items[i] = value
		}
		fmt.Fprintf(w, "\t\treturn vm.ListValue([]vm.Value{%s}), nil\n", strings.Join(items, ", "))
	}
	fmt.Fprintln(w, "\t}))")
	return nil
}

// emitBuzzArgDecode emits the decode statement for arg a at positional index idx.
func emitBuzzArgDecode(w *bytes.Buffer, a std.Arg, idx int) {
	if a.Variadic {
		// Only TypeString variadic is used in practice.
		fmt.Fprintf(w, "\t\t%s := ffi.VariadicStr(bzArgs, %d)\n", a.Name, idx)
		return
	}
	switch a.Type {
	case std.TypeString:
		fmt.Fprintf(w, "\t\t%s := ffi.Str(bzArgs, %d)\n", a.Name, idx)
	case std.TypeInt:
		def := "0"
		if a.Optional && a.Default != nil {
			def = goLiteral(a.Default)
		}
		fmt.Fprintf(w, "\t\t%s := ffi.Int(bzArgs, %d, %s)\n", a.Name, idx, def)
	case std.TypeFloat:
		def := "0"
		if a.Optional && a.Default != nil {
			def = goLiteral(a.Default)
		}
		fmt.Fprintf(w, "\t\t%s := ffi.Float(bzArgs, %d, %s)\n", a.Name, idx, def)
	case std.TypeIndex:
		// Buzz lists are 0-based, matching the Go Impl; no offset.
		fmt.Fprintf(w, "\t\t%s := ffi.Int(bzArgs, %d, 0)\n", a.Name, idx)
	case std.TypeBool:
		def := "false"
		if a.Optional && a.Default != nil {
			def = goLiteral(a.Default)
		}
		fmt.Fprintf(w, "\t\t%s := ffi.Bool(bzArgs, %d, %s)\n", a.Name, idx, def)
	case std.TypeStringSlice:
		fmt.Fprintf(w, "\t\t%s := ffi.StrSlice(bzArgs, %d)\n", a.Name, idx)
	case std.TypeFloatSlice:
		fmt.Fprintf(w, "\t\t%s := ffi.FloatSlice(bzArgs, %d)\n", a.Name, idx)
	case std.TypeByteSlice:
		fmt.Fprintf(w, "\t\t%s := ffi.ByteSlice(bzArgs, %d)\n", a.Name, idx)
	case std.TypeStringSliceSlice:
		fmt.Fprintf(w, "\t\t%s := ffi.StrSliceSlice(bzArgs, %d)\n", a.Name, idx)
	case std.TypeStringMapMap:
		fmt.Fprintf(w, "\t\t%s := ffi.StrMapMap(bzArgs, %d)\n", a.Name, idx)
	case std.TypeStringMap:
		fmt.Fprintf(w, "\t\t%s := ffi.StrMap(bzArgs, %d)\n", a.Name, idx)
	case std.TypeAnyMap:
		fmt.Fprintf(w, "\t\t%s := ffi.AnyMap(bzArgs, %d)\n", a.Name, idx)
	case std.TypeFunc:
		fmt.Fprintf(w, "\t\t%s := ffi.CallbackArg(sess, bzArgs, %d)\n", a.Name, idx)
	case std.TypeAny:
		fmt.Fprintf(w, "\t\t%s := ffi.Any(bzArgs, %d)\n", a.Name, idx)
	default:
		fmt.Fprintf(w, "\t\t%s := ffi.Any(bzArgs, %d) // unsupported type %s\n", a.Name, idx, a.Type.GoType())
	}
}

// optionDecoders collects the option structs a module's opts maps decode into. A method
// whose Impl takes a struct where the descriptor declares a map keeps its Buzz signature;
// the trampoline reads the map into the struct strictly, by the struct's json tags.
type optionDecoders struct {
	types []reflect.Type
}

// add records t and returns the name of the function that decodes it.
func (d *optionDecoders) add(t reflect.Type) (string, error) {
	if _, err := optionFields(t); err != nil {
		return "", err
	}
	if !slices.Contains(d.types, t) {
		d.types = append(d.types, t)
	}
	return "decode" + t.Name(), nil
}

// packages are the import paths of the recorded structs, sorted.
func (d *optionDecoders) packages() []string {
	var out []string
	for _, t := range d.types {
		if !slices.Contains(out, t.PkgPath()) {
			out = append(out, t.PkgPath())
		}
	}
	slices.Sort(out)
	return out
}

// emit writes one decoder per recorded struct. An unknown key or a value of the wrong
// type raises: a dropped `{limt = 5}` would answer a different question with nothing to
// say the typo did not take. A Buzz integer arrives as int64 and a Buzz float as float64.
func (d *optionDecoders) emit(w *bytes.Buffer) {
	for _, t := range d.types {
		fields, _ := optionFields(t)
		keys := make([]string, len(fields))
		for i, f := range fields {
			keys[i] = f.key
		}
		fmt.Fprintf(w, "\nfunc decode%s(member string, in map[string]any) (%s, error) {\n", t.Name(), t)
		fmt.Fprintf(w, "\tvar out %s\n", t)
		fmt.Fprintln(w, "\tfor key, v := range in {")
		fmt.Fprintln(w, "\t\tswitch key {")
		for _, f := range fields {
			fmt.Fprintf(w, "\t\tcase %q:\n", f.key)
			switch f.kind {
			case reflect.Int:
				fmt.Fprintln(w, "\t\t\tswitch x := v.(type) {")
				fmt.Fprintln(w, "\t\t\tcase int64:")
				fmt.Fprintf(w, "\t\t\t\tout.%s = int(x)\n", f.name)
				fmt.Fprintln(w, "\t\t\tcase float64:")
				fmt.Fprintf(w, "\t\t\t\tout.%s = int(x)\n", f.name)
				fmt.Fprintln(w, "\t\t\tcase int:")
				fmt.Fprintf(w, "\t\t\t\tout.%s = x\n", f.name)
				fmt.Fprintln(w, "\t\t\tdefault:")
				w.WriteString("\t\t\t\treturn out, fmt.Errorf(\"%s: %s must be a number, got %T\", member, key, v)\n")
				fmt.Fprintln(w, "\t\t\t}")
			case reflect.String, reflect.Bool:
				fmt.Fprintf(w, "\t\t\tx, ok := v.(%s)\n", f.kind)
				fmt.Fprintln(w, "\t\t\tif !ok {")
				fmt.Fprintf(w, "\t\t\t\treturn out, fmt.Errorf(\"%%s: %%s must be a %s, got %%T\", member, key, v)\n", f.kind)
				fmt.Fprintln(w, "\t\t\t}")
				fmt.Fprintf(w, "\t\t\tout.%s = x\n", f.name)
			}
		}
		fmt.Fprintln(w, "\t\tdefault:")
		fmt.Fprintf(w, "\t\t\treturn out, fmt.Errorf(\"%%s: unknown option %%q (want %s)\", member, key)\n", strings.Join(keys, ", "))
		fmt.Fprintln(w, "\t\t}")
		fmt.Fprintln(w, "\t}")
		fmt.Fprintln(w, "\treturn out, nil")
		fmt.Fprintln(w, "}")
	}
}

// optionField is one key of an option struct: its json name, Go field and kind.
type optionField struct {
	key, name string
	kind      reflect.Kind
}

// optionFields lists t's keys in declaration order. Every exported field needs a json
// name and a kind a Buzz literal can carry (int, string or bool); anything else is an
// error at generation rather than a key the decoder silently cannot set.
func optionFields(t reflect.Type) ([]optionField, error) {
	var out []optionField
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		key, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if key == "" || key == "-" {
			return nil, fmt.Errorf("option struct %s: field %s has no json name to read it by", t, f.Name)
		}
		switch f.Type.Kind() {
		case reflect.Int, reflect.String, reflect.Bool:
			if f.Type.PkgPath() != "" {
				return nil, fmt.Errorf("option struct %s: field %s is the named type %s, which a decoded value cannot be assigned to", t, f.Name, f.Type)
			}
		default:
			return nil, fmt.Errorf("option struct %s: field %s is %s, and an opts map decodes only int, string and bool", t, f.Name, f.Type)
		}
		out = append(out, optionField{key: key, name: f.Name, kind: f.Type.Kind()})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("option struct %s has no field an opts map could set", t)
	}
	return out, nil
}

// returnConv returns the Go expression that converts a method return to a vm.Value.
// Object returns are emitted as direct, typed encoders; scalar and deliberately
// untyped descriptor returns use the small shared primitive helpers.
func returnConv(ret std.Ret, goType reflect.Type, src string, objects *buzzValueEmitter) (string, error) {
	if ret.Nullable {
		if ret.Type != std.TypeString || ret.Object != "" || goType != reflect.TypeFor[*string]() {
			return "", fmt.Errorf("a nullable return is a str? whose Impl returns *string, not %s returning %s", ret.Type.GoType(), goType)
		}
		return fmt.Sprintf("ffi.OptStrVal(%s)", src), nil
	}
	if ret.Object != "" {
		return objects.valueFunc(goType, src)
	}
	return buzzValConv(ret.Type, src), nil
}

// isBoundaryType reports whether rt crosses the typed Buzz boundary.
//
// Two ways to qualify. Anything in the types package does, as before. Registry membership
// is what additionally admits the boundary types declared in spells (Charm, PatchOp,
// Command, Service): they were registered all along, but a package-path test classified a
// host method returning one as a SCALAR, so declaring its Object failed with "its Impl
// returns a scalar", naming neither the cause nor the fix.
//
// The types-package arm is not redundant with the registry: several entries name a
// *Record MIRROR (StatusRecord, CommitRecord) that exists only to be reflected over,
// while the Impl returns the live type beside it, so identity against the registry alone
// would reject exactly the returns this check exists for.
func isBoundaryType(rt reflect.Type) bool {
	if rt.PkgPath() == reflect.TypeFor[types.FileInfo]().PkgPath() {
		return true
	}
	for _, entry := range boundaryTypes {
		if entry.Type == rt {
			return true
		}
	}
	return false
}

// objectName returns the Buzz object a return marshals to, or "" for a scalar.
// It reads the Impl's reflected type at generation time. Runtime conversion is
// emitted as direct field access in internal/interp/bindings/gen.
func objectName(goType reflect.Type) string {
	switch {
	case goType.Kind() == reflect.Struct && isBoundaryType(goType):
		return buzzObjectName(goType)
	case goType.Kind() == reflect.Slice && goType.Elem().Kind() == reflect.Struct && isBoundaryType(goType.Elem()):
		// Bracketed, so the descriptor states the shape as a magusfile annotation
		// spells it and no consumer has to reflect to recover the list-ness.
		return fmt.Sprintf("[%s]", buzzObjectName(goType.Elem()))
	}
	return ""
}

// buzzObjectName is the public Buzz object name for t, resolved through the boundary
// registry. A Go type sometimes keeps a descriptive suffix while the Buzz API exposes
// the concise domain name: ProjectsOutput is Projects, ImpactResult is Impact.
//
// This USED to be a hand-written switch listing each rename, which made the registry and
// the switch two sources of one truth: adding a renamed type compiled fine and then failed
// at generate time with "declares Object X but its Impl returns Y", naming neither the
// switch nor the fix. The registry already carries the mapping, so read it there.
func buzzObjectName(t reflect.Type) string {
	return buzzName(t)
}

// checkObjectDecls verifies every method's declared Ret.Object against the Impl's
// actual return type, and returns one error naming every disagreement.
//
// This is what licenses stating the object name in the descriptor at all. The name
// is already derivable by reflection, so a hand-written copy is only safe while
// something proves the copy right. Otherwise it is a second source of truth that
// drifts silently, and a wrong object name is worse than none: it tells an author to
// annotate `> FileInfo` on a call that returns ExecResult, and the checker then
// rejects correct code.
//
// Codegen is the right place to fail. It runs on every `magus run generate`, it
// already has the reflection in hand, and a build-time error costs a developer one
// message where a runtime one costs a user a confusing load failure.
func checkObjectDecls(mods []std.Module) error {
	var problems []string
	for _, mod := range mods {
		for _, meth := range mod.Methods {
			if len(meth.Returns) != 1 || meth.Impl == nil {
				continue
			}
			want := objectName(reflect.TypeOf(meth.Impl).Out(0))
			got := meth.Returns[0].Object
			if want == got {
				continue
			}
			switch {
			case want == "":
				problems = append(problems, fmt.Sprintf("%s\\%s: declares Object %q but its Impl returns a scalar",
					mod.Name, meth.Name, got))
			case got == "":
				problems = append(problems, fmt.Sprintf("%s\\%s: Impl returns object %s, add Object: %q to its Ret",
					mod.Name, meth.Name, want, want))
			default:
				problems = append(problems, fmt.Sprintf("%s\\%s: declares Object %q but its Impl returns %s",
					mod.Name, meth.Name, got, want))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("object return declarations disagree with their Impls: %s", strings.Join(problems, ", "))
	}
	return nil
}

// buzzValConv returns the Go expression that converts src (of the given
// TypeTag) to a vm.Value.
func buzzValConv(t std.TypeTag, src string) string {
	switch t {
	case std.TypeString:
		return fmt.Sprintf("ffi.StrVal(%s)", src)
	case std.TypeInt, std.TypeIndex:
		return fmt.Sprintf("ffi.IntVal(%s)", src)
	case std.TypeBool:
		return fmt.Sprintf("ffi.BoolVal(%s)", src)
	case std.TypeFloat:
		return fmt.Sprintf("ffi.FloatVal(%s)", src)
	case std.TypeStringSlice:
		return fmt.Sprintf("ffi.StrSliceVal(%s)", src)
	case std.TypeFloatSlice:
		return fmt.Sprintf("ffi.FloatSliceVal(%s)", src)
	case std.TypeByteSlice:
		return fmt.Sprintf("ffi.ByteSliceVal(%s)", src)
	case std.TypeStringSliceSlice:
		return fmt.Sprintf("ffi.StrSliceSliceVal(%s)", src)
	case std.TypeStringMapMap:
		return fmt.Sprintf("ffi.StrMapMapVal(%s)", src)
	case std.TypeStringMap:
		return fmt.Sprintf("ffi.StrMapVal(%s)", src)
	case std.TypeAnyMap:
		return fmt.Sprintf("ffi.AnyMapVal(%s)", src)
	case std.TypeAny:
		return fmt.Sprintf("ffi.AnyVal(%s)", src)
	default:
		panic(fmt.Sprintf("buzzValConv: unsupported type tag %s for value conversion", t.GoType()))
	}
}

// buzzValueEmitter writes concrete Go-to-VM encoders for boundary types. It is
// generator-only reflection: the binary receives ordinary field reads, loops, and vm
// constructors.
//
// One emitter serves the whole package, and its output is one file. Per-module
// emitters produced a copy of each shared type's encoder in every module that
// returned it (Path had three), and none of them could be reached from the
// hand-bound members in the parent package, which is why types carried a second,
// parallel set of encoders as METHODS until this replaced them.
type buzzValueEmitter struct {
	funcs     bytes.Buffer
	emitted   map[reflect.Type]bool
	enumReach map[reflect.Type]bool
	// pkgs holds the boundaryPackages aliases the encoders spell, for importLines.
	pkgs     map[string]bool
	usesTime bool
	usesFFI  bool
	// usesEnums records that a call this emitter rendered passes `enums`, so the
	// trampoline file must declare it.
	usesEnums bool
}

func newBuzzValueEmitter() *buzzValueEmitter {
	return &buzzValueEmitter{emitted: map[reflect.Type]bool{}, enumReach: map[reflect.Type]bool{}, pkgs: map[string]bool{}}
}

func (e *buzzValueEmitter) valueFunc(t reflect.Type, src string) (string, error) {
	switch t.Kind() {
	case reflect.Struct:
		if err := e.emitStruct(t); err != nil {
			return "", err
		}
		return e.call(t, src), nil
	case reflect.Slice:
		if t.Elem().Kind() != reflect.Struct {
			return "", fmt.Errorf("object return %s is not a slice of structs", t)
		}
		if err := e.emitStruct(t.Elem()); err != nil {
			return "", err
		}
		if e.reachesEnum(t.Elem()) {
			return fmt.Sprintf("ffi.ObjectSlice(%s, func(v %s) vm.Value { return %s })", src, e.qualify(t.Elem()), e.call(t.Elem(), "v")), nil
		}
		return fmt.Sprintf("ffi.ObjectSlice(%s, %s)", src, e.funcName(t.Elem())), nil
	default:
		return "", fmt.Errorf("object return %s is not a struct or slice of structs", t)
	}
}

func (e *buzzValueEmitter) emitStruct(t reflect.Type) error {
	if e.emitted[t] {
		return nil
	}
	if !isBoundaryType(t) {
		return fmt.Errorf("object %s is not declared in the boundary registry (cmd/magus-utils/boundary_types.go)", t)
	}
	// Mark before visiting fields so a mistaken recursive value type terminates
	// deterministically and produces a normal Go compile error rather than looping.
	e.emitted[t] = true
	e.markPkg(t)

	var body bytes.Buffer
	if e.reachesEnum(t) {
		e.usesFFI = true
		fmt.Fprintf(&body, "func %s(v %s, enums ffi.EnumCases) vm.Value {\n", e.funcName(t), e.qualify(t))
	} else {
		fmt.Fprintf(&body, "func %s(v %s) vm.Value {\n", e.funcName(t), e.qualify(t))
	}
	fmt.Fprintln(&body, "\tout := vm.NewMap()")
	for _, bf := range boundaryFields(t) {
		f := bf.StructField
		value, err := e.value(&body, "v."+bf.selector, f.Name, f.Type, "\t")
		if err != nil {
			return fmt.Errorf("%s.%s: %w", t.Name(), f.Name, err)
		}
		fmt.Fprintf(&body, "\tout.MapSet(%q, %s)\n", buzzVMFieldName(f), value)
	}
	fmt.Fprintln(&body, "\treturn out")
	fmt.Fprintln(&body, "}")
	fmt.Fprintln(&body)

	// Nested struct encoders must precede neither callers nor declarations in Go,
	// so append the current function after recursively discovered helpers.
	e.funcs.Write(body.Bytes())
	return nil
}

// boundaryPackage is a Go package boundary types may live in: the alias a generated file
// imports it under, and the prefix that keeps its encoder and decoder names apart from a
// same-named type in another package (queue's Check beside types.Check).
type boundaryPackage struct {
	path   string
	alias  string
	prefix string
}

// boundaryPackages is every package a boundary type may come from, keyed by import path
// rather than by name: two of them are named types. Their order is the generated import
// order.
var boundaryPackages = []boundaryPackage{
	{path: "github.com/egladman/magus/spells", alias: "spells"},
	{path: "github.com/egladman/magus/types", alias: "types"},
	{path: "github.com/egladman/magus/internal/queue/types", alias: "queuetypes", prefix: "Queue"},
}

// typePackage is t's entry in boundaryPackages. A type from any other package panics: the
// generator would otherwise emit a spelling no generated file imports.
func typePackage(t reflect.Type) boundaryPackage {
	for _, p := range boundaryPackages {
		if p.path == t.PkgPath() {
			return p
		}
	}
	panic(fmt.Sprintf("boundary type %s is in package %q, which boundaryPackages (cmd/magus-utils/bindings.go) does not list", t, t.PkgPath()))
}

// qualifyType renders t as a generated file spells it ("types.FileInfo",
// "queuetypes.Change").
func qualifyType(t reflect.Type) string {
	return fmt.Sprintf("%s.%s", typePackage(t).alias, t.Name())
}

// typeIdent names t in a generated identifier (ObjectFileInfo, DecodeQueueChange).
func typeIdent(t reflect.Type) string {
	return typePackage(t).prefix + t.Name()
}

// importLines renders the import of each package in used, which is keyed by alias, in
// boundaryPackages order. An alias the import path already implies is not written.
func importLines(used map[string]bool) string {
	var b strings.Builder
	for _, p := range boundaryPackages {
		if !used[p.alias] {
			continue
		}
		if path.Base(p.path) == p.alias {
			fmt.Fprintf(&b, "\t%q\n", p.path)
		} else {
			fmt.Fprintf(&b, "\t%s %q\n", p.alias, p.path)
		}
	}
	return b.String()
}

// qualify renders t as the emitted file must spell it, and markPkg records the import
// that spelling needs. Together they are what let a boundary type live outside the types
// package.
func (e *buzzValueEmitter) qualify(t reflect.Type) string {
	return qualifyType(t)
}

func (e *buzzValueEmitter) markPkg(t reflect.Type) {
	e.pkgs[typePackage(t).alias] = true
}

// value renders the conversion for one field, emitting any temporaries it needs first.
// path names the field being rendered ("Targets", and "TargetsItem" one level into its
// element) and is what every temporary is named after. See name.
func (e *buzzValueEmitter) value(w *bytes.Buffer, value, path string, t reflect.Type, indent string) (string, error) {
	if t == reflect.TypeFor[time.Time]() {
		e.usesTime = true
		formatted := e.name("formatted", path)
		fmt.Fprintf(w, "%s%s := \"\"\n", indent, formatted)
		fmt.Fprintf(w, "%sif !%s.IsZero() {\n", indent, value)
		fmt.Fprintf(w, "%s\t%s = %s.Format(time.RFC3339Nano)\n", indent, formatted, value)
		fmt.Fprintf(w, "%s}\n", indent)
		return fmt.Sprintf("vm.StrValue(%s)", formatted), nil
	}

	if en, ok := buzzEnum(t); ok {
		return fmt.Sprintf("enums.Case(%q, string(%s))", en.Name, value), nil
	}

	switch t.Kind() {
	case reflect.String:
		// A NAMED string (types.CheckStatus, types.TargetRunState) is a distinct
		// Go type, so it needs the conversion every numeric case already makes. Plain
		// string is emitted bare rather than as string(s), which is redundant and what
		// the unconvert linter exists to catch.
		if t != reflect.TypeOf("") {
			return fmt.Sprintf("vm.StrValue(string(%s))", value), nil
		}
		return fmt.Sprintf("vm.StrValue(%s)", value), nil
	case reflect.Bool:
		return fmt.Sprintf("vm.BoolValue(%s)", value), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return fmt.Sprintf("vm.IntValue(int64(%s))", value), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return fmt.Sprintf("vm.IntValue(int64(%s))", value), nil
	case reflect.Float32, reflect.Float64:
		return fmt.Sprintf("vm.FloatValue(float64(%s))", value), nil
	case reflect.Struct:
		if err := e.emitStruct(t); err != nil {
			return "", err
		}
		return e.call(t, value), nil
	case reflect.Slice, reflect.Array:
		out, index := e.name("items", path), e.name("index", path)
		fmt.Fprintf(w, "%s%s := make([]vm.Value, len(%s))\n", indent, out, value)
		fmt.Fprintf(w, "%sfor %s := range %s {\n", indent, index, value)
		item, err := e.value(w, fmt.Sprintf("%s[%s]", value, index), path+"Item", t.Elem(), indent+"\t")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(w, "%s\t%s[%s] = %s\n", indent, out, index, item)
		fmt.Fprintf(w, "%s}\n", indent)
		return fmt.Sprintf("vm.ListValue(%s)", out), nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return "", fmt.Errorf("map key %s is not a string", t.Key())
		}
		out, key, item := e.name("mapped", path), e.name("key", path), e.name("item", path)
		fmt.Fprintf(w, "%s%s := vm.NewMap()\n", indent, out)
		fmt.Fprintf(w, "%sfor %s, %s := range %s {\n", indent, key, item, value)
		converted, err := e.value(w, item, path+"Value", t.Elem(), indent+"\t")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(w, "%s\t%s.MapSet(%s, %s)\n", indent, out, key, converted)
		fmt.Fprintf(w, "%s}\n", indent)
		return out, nil
	case reflect.Pointer:
		// An optional field: nil crosses the boundary as Buzz null, matching the `T?`
		// the mirror declares. A nil-checked temporary rather than an inline
		// expression, because the element may need statements of its own (a time
		// needs formatting, a struct its own conversion function).
		out := e.name("opt", path)
		fmt.Fprintf(w, "%s%s := vm.Null\n", indent, out)
		fmt.Fprintf(w, "%sif %s != nil {\n", indent, value)
		elem, err := e.value(w, fmt.Sprintf("(*%s)", value), path, t.Elem(), indent+"\t")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(w, "%s\t%s = %s\n", indent, out, elem)
		fmt.Fprintf(w, "%s}\n", indent)
		return out, nil
	case reflect.Interface:
		// The field holds a JSON-shaped value of no declared type (host config a harness
		// merges), so it converts by its dynamic type, the way any other untyped host
		// value does; the mirror declares it `any?`.
		e.usesFFI = true
		return fmt.Sprintf("ffi.AnyVal(%s)", value), nil
	default:
		return "", fmt.Errorf("unsupported field type %s", t)
	}
}

// funcName keys the encoder on the GO type name, not the Buzz one. The Buzz name reads
// better at a call site (ObjectProjects over ObjectProjectsOutput), and it is not unique:
// several registry entries name a *Record MIRROR that exists only to be reflected over
// (CommitRecord is Buzz `Commit`) while the Impl returns the live type beside it, so both
// arrive here wanting the same name.
//
// Exported because the hand-bound members in internal/interp/bindings call these
// directly; a member bound at run time has no trampoline to inline the conversion into.
func (e *buzzValueEmitter) funcName(t reflect.Type) string {
	return "Object" + typeIdent(t)
}

// call renders a call of t's encoder on src, passing `enums` through when t reaches an
// enum field.
func (e *buzzValueEmitter) call(t reflect.Type, src string) string {
	if e.reachesEnum(t) {
		e.usesEnums = true
		return fmt.Sprintf("%s(%s, enums)", e.funcName(t), src)
	}
	return fmt.Sprintf("%s(%s)", e.funcName(t), src)
}

// reachesEnum reports whether encoding t writes an enum field anywhere below it. Such
// an encoder takes the session's ffi.EnumCases: an enum case is interned on the
// definition the session holds, so no session-free encoder can produce one, and a
// bare str in its place compares unequal to every case a magusfile names.
func (e *buzzValueEmitter) reachesEnum(t reflect.Type) bool {
	if reach, seen := e.enumReach[t]; seen {
		return reach
	}
	reach := e.walkEnums(t, map[reflect.Type]bool{})
	e.enumReach[t] = reach
	return reach
}

// walkEnums is reachesEnum's search. It caches only a true answer on the way down:
// a false one found while a type that contains itself was being visited may be false
// only because that cycle was cut.
func (e *buzzValueEmitter) walkEnums(t reflect.Type, visiting map[reflect.Type]bool) bool {
	if reach, seen := e.enumReach[t]; seen {
		return reach
	}
	if visiting[t] {
		return false
	}
	visiting[t] = true
	reach := false
	switch t.Kind() {
	case reflect.String:
		_, reach = buzzEnum(t)
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		reach = e.walkEnums(t.Elem(), visiting)
	case reflect.Struct:
		for _, bf := range boundaryFields(t) {
			if e.walkEnums(bf.Type, visiting) {
				reach = true
				break
			}
		}
	}
	if reach {
		e.enumReach[t] = true
	}
	return reach
}

// name builds a temporary's identifier from the field path rather than a running
// counter. A counter makes every temporary after an inserted field shift by one, so
// adding a single field rewrites the rest of the file and the real change is lost in
// the renumbering. Naming after the path keeps a temporary stable under edits
// elsewhere and says what it holds: itemsTargets, not items78. Field names are unique
// within a struct, and each nesting level extends the path, so the result is unique
// within the function.
func (e *buzzValueEmitter) name(prefix, path string) string {
	return prefix + path
}

// boundaryField is one field a boundary type carries across, with the selector that
// reaches it from the value ("Schema.Version" for a field of an embedded struct).
type boundaryField struct {
	reflect.StructField
	selector string
}

// boundaryFields lists t's exported fields the way buzzgen's mirror declares them: an
// untagged embedded struct contributes its own fields inline rather than one nested field.
func boundaryFields(t reflect.Type) []boundaryField {
	var out []boundaryField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || f.Tag.Get("buzz") == "-" {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Tag.Get("buzz") == "" {
			for _, inner := range boundaryFields(f.Type) {
				inner.selector = fmt.Sprintf("%s.%s", f.Name, inner.selector)
				out = append(out, inner)
			}
			continue
		}
		out = append(out, boundaryField{StructField: f, selector: f.Name})
	}
	return out
}

// buzzVMFieldName must agree with the mirror's field name exactly: this emits the RUNTIME
// map key and buzzgen.ObjectDecl emits the DECLARED field, so the two drifting means a
// magusfile reads a name the encoder never writes. Both go through LowerFirstWord for that
// reason.
func buzzVMFieldName(f reflect.StructField) string {
	if name := f.Tag.Get("buzz"); name != "" {
		return name
	}
	return buzzgen.LowerFirstWord(f.Name)
}
