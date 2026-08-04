// Subcommand `bindings` emits per-VM trampoline code from std.Module
// declarations. The generated file registers the module's methods on a backend
// Session: decode args, call the Impl in the host package, encode results.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"reflect"
	"strings"
	"time"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/std"
	"github.com/egladman/magus/types"
)

// wasmExcludedModules names the host modules whose generated trampolines are
// tagged //go:build !wasm: the IO leaves (process, filesystem, network, env, vcs,
// and the magus meta-module) whose std Impls don't compile for wasm, so the
// browser playground never registers them. Keep in sync with the //go:build !wasm
// tags on the matching std/<name>.go files and with dry.WASMCompatibleMagusModules
// (whose complement this is).
var wasmExcludedModules = map[string]bool{
	"fs":      true,
	"os":      true,
	"http":    true,
	"archive": true,
	"vcs":     true,
	"magus":   true,
}

func runBindings(args []string) error {
	fs := flag.NewFlagSet("bindings", flag.ExitOnError)
	moduleName := fs.String("module", "", "module name to generate (e.g. fs)")
	lang := fs.String("lang", "buzz", "codegen target: buzz")
	outPath := fs.String("out", "", "output file path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *moduleName == "" || *outPath == "" {
		return fmt.Errorf("usage: magus-utils bindings -module <name> -lang buzz -out <path>")
	}

	m, ok := std.Get(*moduleName)
	if !ok {
		return fmt.Errorf("unknown module %q", *moduleName)
	}

	if *lang != "buzz" {
		return fmt.Errorf("unknown lang %q (have: buzz)", *lang)
	}

	// Checked across EVERY module, not just the one being generated: the
	// declarations are one table, and a per-module check would report a mismatch
	// only when that module happened to be the regenerate target.
	if err := checkObjectDecls(std.All()); err != nil {
		return err
	}
	out, err := emitBuzz(m)
	if err != nil {
		return fmt.Errorf("emit: %w", err)
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

// emitBuzz emits the Buzz trampolines for module m into package gen
// (internal/interp/bindings/gen):
// one Register<Module>(ctx, sess) that builds a buzz map of DirectValue closures.
// Buzz closures return (Value, error) natively, so host errors propagate as a Buzz
// runtime error instead of a panic. Method names stay snake_case to match the
// magusfile DSL; multi-return Impls yield a list.
//
// It emits directly against the concrete magus/gopherbuzz value system. Typed
// object returns get a concrete, generated encoder in this package: no reflection
// and no map[string]any intermediary are linked into the magus runtime path.
func emitBuzz(m std.Module) ([]byte, error) {
	// Build the func body first, then derive the import set from what it uses:
	// std (Impls/resolvers) appears only when the body references it, so an unused
	// import can't sneak in.
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

	for _, f := range m.Fields {
		emitBuzzField(&body, f)
	}
	objects := newBuzzValueEmitter(m.Name)
	for _, meth := range m.Methods {
		if err := emitBuzzMethod(&body, m, meth, objects); err != nil {
			return nil, err
		}
	}

	fmt.Fprintln(&body, "\treturn m")
	fmt.Fprintln(&body, "}")

	var b bytes.Buffer
	// The IO-heavy modules (fs/os/http/archive/env/vcs/magus) call std functions
	// that are themselves excluded from the wasm build, so their trampolines can't
	// compile there; tag them off wasm to match. The browser playground registers
	// only the pure-compute modules (dry.WASMCompatibleMagusModules), which stay in
	// every build.
	if wasmExcludedModules[m.Name] {
		fmt.Fprintln(&b, "//go:build !wasm")
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b, "// Code generated by magus-utils bindings. DO NOT EDIT.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "package gen")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, `import (`)
	fmt.Fprintln(&b, `	"context"`)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, `	buzz "github.com/egladman/magus/libs/gopherbuzz"`)
	fmt.Fprintln(&b, `	vm "github.com/egladman/magus/libs/gopherbuzz/vm"`)
	if bytes.Contains(body.Bytes(), []byte("std.")) {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/std"`)
	}
	if objects.usesTypes {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/types"`)
	}
	if objects.usesTime {
		fmt.Fprintln(&b, `	"time"`)
	}
	fmt.Fprintln(&b, `)`)
	fmt.Fprintln(&b)
	b.Write(body.Bytes())
	b.Write(objects.funcs.Bytes())

	out, err := format.Source(b.Bytes())
	if err != nil {
		// Return nil on failure: the unformatted buffer is known-bad Go.
		return nil, fmt.Errorf("gofmt: %w\n--- source ---\n%s", err, b.String())
	}
	return out, nil
}

// emitBuzzField resolves a static field once and stores it on the module map.
// The key is camelCased (Buzz convention) though the std descriptor is snake_case.
func emitBuzzField(w *bytes.Buffer, f std.Field) {
	callArgs := ""
	if std.FieldResolverTakesCtx(f) {
		callArgs = "ctx"
	}
	fmt.Fprintf(w, "\tif v, err := std.%s(%s); err == nil {\n", std.FieldFuncName(f), callArgs)
	fmt.Fprintf(w, "\t\tm.MapSet(%q, %s)\n", std.CamelCase(f.Name), buzzValConv(f.Type, "v"))
	fmt.Fprintln(w, "\t}")
}

// emitBuzzMethod emits one DirectValue trampoline for meth. The closure's arg
// slice is named bzArgs to avoid colliding with host args named "args". The map
// key is camelCased to match Buzz's convention (the snake_case descriptor name
// stays the runtime label so errors still read e.g. "fs.readFile").
func emitBuzzMethod(w *bytes.Buffer, m std.Module, meth std.Method, objects *buzzValueEmitter) error {
	name := std.CamelCase(meth.Name)
	if meth.BuzzName != "" {
		name = meth.BuzzName
	}
	fmt.Fprintf(w, "\tm.MapSet(%q, vm.DirectValue(%q, func(ctx context.Context, bzArgs []vm.Value) (vm.Value, error) {\n",
		name, m.Name+"."+name)

	for i, a := range meth.Args {
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
		fmt.Fprintf(w, "\t\tif err := std.%s(%s); err != nil {\n", std.MethodFuncName(meth), callStr)
		fmt.Fprintln(w, "\t\t\treturn vm.Null, HostError(err)")
		fmt.Fprintln(w, "\t\t}")
		fmt.Fprintln(w, "\t\treturn vm.Null, nil")
	case 1:
		fmt.Fprintf(w, "\t\tret0, err := std.%s(%s)\n", std.MethodFuncName(meth), callStr)
		fmt.Fprintln(w, "\t\tif err != nil {")
		fmt.Fprintln(w, "\t\t\treturn vm.Null, HostError(err)")
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
		fmt.Fprintf(w, "\t\t%s := std.%s(%s)\n", strings.Join(lhsParts, ", "), std.MethodFuncName(meth), callStr)
		fmt.Fprintln(w, "\t\tif err != nil {")
		fmt.Fprintln(w, "\t\t\treturn vm.Null, HostError(err)")
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
		fmt.Fprintf(w, "\t\t%s := VariadicStr(bzArgs, %d)\n", a.Name, idx)
		return
	}
	switch a.Type {
	case std.TypeString:
		fmt.Fprintf(w, "\t\t%s := Str(bzArgs, %d)\n", a.Name, idx)
	case std.TypeInt:
		def := "0"
		if a.Optional && a.Default != nil {
			def = goLiteral(a.Default)
		}
		fmt.Fprintf(w, "\t\t%s := Int(bzArgs, %d, %s)\n", a.Name, idx, def)
	case std.TypeFloat:
		def := "0"
		if a.Optional && a.Default != nil {
			def = goLiteral(a.Default)
		}
		fmt.Fprintf(w, "\t\t%s := Float(bzArgs, %d, %s)\n", a.Name, idx, def)
	case std.TypeIndex:
		// Buzz lists are 0-based, matching the Go Impl; no offset.
		fmt.Fprintf(w, "\t\t%s := Int(bzArgs, %d, 0)\n", a.Name, idx)
	case std.TypeBool:
		def := "false"
		if a.Optional && a.Default != nil {
			def = goLiteral(a.Default)
		}
		fmt.Fprintf(w, "\t\t%s := Bool(bzArgs, %d, %s)\n", a.Name, idx, def)
	case std.TypeStringSlice:
		fmt.Fprintf(w, "\t\t%s := StrSlice(bzArgs, %d)\n", a.Name, idx)
	case std.TypeStringMap:
		fmt.Fprintf(w, "\t\t%s := StrMap(bzArgs, %d)\n", a.Name, idx)
	case std.TypeAnyMap:
		fmt.Fprintf(w, "\t\t%s := AnyMap(bzArgs, %d)\n", a.Name, idx)
	case std.TypeFunc:
		fmt.Fprintf(w, "\t\t%s := CallbackArg(sess, bzArgs, %d)\n", a.Name, idx)
	case std.TypeAny:
		fmt.Fprintf(w, "\t\t%s := Any(bzArgs, %d)\n", a.Name, idx)
	default:
		fmt.Fprintf(w, "\t\t%s := Any(bzArgs, %d) // unsupported type %s\n", a.Name, idx, a.Type.GoType())
	}
}

// returnConv returns the Go expression that converts a method return to a vm.Value.
// Object returns are emitted as direct, typed encoders; scalar and deliberately
// untyped descriptor returns use the small shared primitive helpers.
func returnConv(ret std.Ret, goType reflect.Type, src string, objects *buzzValueEmitter) (string, error) {
	if ret.Object != "" {
		return objects.valueFunc(goType, src)
	}
	return buzzValConv(ret.Type, src), nil
}

// objectName returns the Buzz object a return marshals to, or "" for a scalar.
// It reads the Impl's reflected type at generation time. Runtime conversion is
// emitted as direct field access in internal/interp/bindings/gen.
func objectName(goType reflect.Type) string {
	switch {
	case goType.Kind() == reflect.Struct && goType.PkgPath() == reflect.TypeFor[types.FileInfo]().PkgPath():
		return buzzObjectName(goType)
	case goType.Kind() == reflect.Slice && goType.Elem().Kind() == reflect.Struct && goType.Elem().PkgPath() == reflect.TypeFor[types.FileInfo]().PkgPath():
		// Bracketed, so the descriptor states the shape as a magusfile annotation
		// spells it and no consumer has to reflect to recover the list-ness.
		return "[" + buzzObjectName(goType.Elem()) + "]"
	}
	return ""
}

// buzzObjectName is the public Buzz object name for t. The generated mirror is
// authoritative: a Go type sometimes keeps a descriptive suffix while the Buzz
// surface exposes the concise domain name.
func buzzObjectName(t reflect.Type) string {
	switch t {
	case reflect.TypeFor[types.VCSTag]():
		return "Tag"
	case reflect.TypeFor[types.HTTPResponse]():
		return "HttpResponse"
	case reflect.TypeFor[types.ProjectsOutput]():
		return "Projects"
	case reflect.TypeFor[types.AffectedResult]():
		return "Affected"
	case reflect.TypeFor[types.GraphView]():
		return "Graph"
	case reflect.TypeFor[types.TargetGraphOutput]():
		return "TargetGraph"
	}
	return t.Name()
}

// checkObjectDecls verifies every method's declared Ret.Object against the Impl's
// actual return type, and returns one error naming every disagreement.
//
// This is what licenses stating the object name in the descriptor at all. The name
// is already derivable by reflection, so a hand-written copy is only safe while
// something proves the copy right - otherwise it is a second source of truth that
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
				problems = append(problems, fmt.Sprintf("%s\\%s: Impl returns object %s; add Object: %q to its Ret",
					mod.Name, meth.Name, want, want))
			default:
				problems = append(problems, fmt.Sprintf("%s\\%s: declares Object %q but its Impl returns %s",
					mod.Name, meth.Name, got, want))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("object return declarations disagree with their Impls:\n  %s", strings.Join(problems, "\n  "))
	}
	return nil
}

// buzzValConv returns the Go expression that converts src (of the given
// TypeTag) to a vm.Value.
func buzzValConv(t std.TypeTag, src string) string {
	switch t {
	case std.TypeString:
		return fmt.Sprintf("StrVal(%s)", src)
	case std.TypeInt, std.TypeIndex:
		return fmt.Sprintf("IntVal(%s)", src)
	case std.TypeBool:
		return fmt.Sprintf("BoolVal(%s)", src)
	case std.TypeFloat:
		return fmt.Sprintf("FloatVal(%s)", src)
	case std.TypeStringSlice:
		return fmt.Sprintf("StrSliceVal(%s)", src)
	case std.TypeStringMap:
		return fmt.Sprintf("StrMapVal(%s)", src)
	case std.TypeAnyMap:
		return fmt.Sprintf("AnyMapVal(%s)", src)
	case std.TypeAny:
		return fmt.Sprintf("AnyVal(%s)", src)
	default:
		panic(fmt.Sprintf("buzzValConv: unsupported type tag %s for value conversion", t.GoType()))
	}
}

// buzzValueEmitter writes concrete Go-to-VM encoders for descriptor object
// returns. It is generator-only reflection: the binary receives ordinary field
// reads, loops, and vm constructors.
type buzzValueEmitter struct {
	funcs     bytes.Buffer
	emitted   map[reflect.Type]bool
	module    string
	usesTypes bool
	usesTime  bool
	n         int
}

func newBuzzValueEmitter(module string) *buzzValueEmitter {
	return &buzzValueEmitter{emitted: map[reflect.Type]bool{}, module: titleCase(module)}
}

func (e *buzzValueEmitter) valueFunc(t reflect.Type, src string) (string, error) {
	switch t.Kind() {
	case reflect.Struct:
		if err := e.emitStruct(t); err != nil {
			return "", err
		}
		return e.funcName(t) + "(" + src + ")", nil
	case reflect.Slice:
		if t.Elem().Kind() != reflect.Struct {
			return "", fmt.Errorf("object return %s is not a slice of structs", t)
		}
		if err := e.emitSlice(t.Elem()); err != nil {
			return "", err
		}
		return e.sliceFuncName(t.Elem()) + "(" + src + ")", nil
	default:
		return "", fmt.Errorf("object return %s is not a struct or slice of structs", t)
	}
}

func (e *buzzValueEmitter) emitStruct(t reflect.Type) error {
	if e.emitted[t] {
		return nil
	}
	if t.PkgPath() != reflect.TypeFor[types.FileInfo]().PkgPath() {
		return fmt.Errorf("object %s is outside the types package", t)
	}
	// Mark before visiting fields so a mistaken recursive value type terminates
	// deterministically and produces a normal Go compile error rather than looping.
	e.emitted[t] = true
	e.usesTypes = true

	var body bytes.Buffer
	fmt.Fprintf(&body, "func %s(v types.%s) vm.Value {\n", e.funcName(t), t.Name())
	fmt.Fprintln(&body, "\tout := vm.NewMap()")
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || f.Tag.Get("buzz") == "-" {
			continue
		}
		value, err := e.value(&body, "v."+f.Name, f.Type, "\t")
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

func (e *buzzValueEmitter) emitSlice(t reflect.Type) error {
	if err := e.emitStruct(t); err != nil {
		return err
	}
	marker := reflect.SliceOf(t)
	if e.emitted[marker] {
		return nil
	}
	e.emitted[marker] = true
	fmt.Fprintf(&e.funcs, "func %s(values []types.%s) vm.Value {\n", e.sliceFuncName(t), t.Name())
	fmt.Fprintln(&e.funcs, "\titems := make([]vm.Value, len(values))")
	fmt.Fprintln(&e.funcs, "\tfor i, value := range values {")
	fmt.Fprintf(&e.funcs, "\t\titems[i] = %s(value)\n", e.funcName(t))
	fmt.Fprintln(&e.funcs, "\t}")
	fmt.Fprintln(&e.funcs, "\treturn vm.ListValue(items)")
	fmt.Fprintln(&e.funcs, "}")
	fmt.Fprintln(&e.funcs)
	return nil
}

func (e *buzzValueEmitter) value(w *bytes.Buffer, value string, t reflect.Type, indent string) (string, error) {
	if t == reflect.TypeFor[time.Time]() {
		e.usesTime = true
		formatted := e.name("formatted")
		fmt.Fprintf(w, "%s%s := \"\"\n", indent, formatted)
		fmt.Fprintf(w, "%sif !%s.IsZero() {\n", indent, value)
		fmt.Fprintf(w, "%s\t%s = %s.Format(time.RFC3339)\n", indent, formatted, value)
		fmt.Fprintf(w, "%s}\n", indent)
		return "vm.StrValue(" + formatted + ")", nil
	}

	switch t.Kind() {
	case reflect.String:
		return "vm.StrValue(" + value + ")", nil
	case reflect.Bool:
		return "vm.BoolValue(" + value + ")", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "vm.IntValue(int64(" + value + "))", nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "vm.IntValue(int64(" + value + "))", nil
	case reflect.Float32, reflect.Float64:
		return "vm.FloatValue(float64(" + value + "))", nil
	case reflect.Struct:
		if err := e.emitStruct(t); err != nil {
			return "", err
		}
		return e.funcName(t) + "(" + value + ")", nil
	case reflect.Slice, reflect.Array:
		out, index := e.name("items"), e.name("index")
		fmt.Fprintf(w, "%s%s := make([]vm.Value, len(%s))\n", indent, out, value)
		fmt.Fprintf(w, "%sfor %s := range %s {\n", indent, index, value)
		item, err := e.value(w, value+"["+index+"]", t.Elem(), indent+"\t")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(w, "%s\t%s[%s] = %s\n", indent, out, index, item)
		fmt.Fprintf(w, "%s}\n", indent)
		return "vm.ListValue(" + out + ")", nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return "", fmt.Errorf("map key %s is not a string", t.Key())
		}
		out, key, item := e.name("mapped"), e.name("key"), e.name("item")
		fmt.Fprintf(w, "%s%s := vm.NewMap()\n", indent, out)
		fmt.Fprintf(w, "%sfor %s, %s := range %s {\n", indent, key, item, value)
		converted, err := e.value(w, item, t.Elem(), indent+"\t")
		if err != nil {
			return "", err
		}
		fmt.Fprintf(w, "%s\t%s.MapSet(%s, %s)\n", indent, out, key, converted)
		fmt.Fprintf(w, "%s}\n", indent)
		return out, nil
	default:
		return "", fmt.Errorf("unsupported field type %s", t)
	}
}

func (e *buzzValueEmitter) funcName(t reflect.Type) string {
	return "buzzValue" + e.module + t.Name()
}

func (e *buzzValueEmitter) sliceFuncName(t reflect.Type) string { return e.funcName(t) + "Slice" }

func (e *buzzValueEmitter) name(prefix string) string {
	e.n++
	return fmt.Sprintf("%s%d", prefix, e.n)
}

func buzzVMFieldName(f reflect.StructField) string {
	if name := f.Tag.Get("buzz"); name != "" {
		return name
	}
	return strings.ToLower(f.Name[:1]) + f.Name[1:]
}
