// Package host is the single source of truth for host-binding APIs that
// magusfiles call into. Each module (os, fs, vcs, …) declares its
// Methods here as a Module value with typed args, return types, and a Go
// Impl. The magus-bindings-gen tool consumes these declarations and emits the
// Buzz trampolines into gen/buzz from the same Impl.
package std

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
)

// Callback is the host-side handle for a VM-side function value passed as
// an argument. gen/buzz wraps a buzz.Session + function value.
// Impls invoke the callback via Call; args are marshalled per VM convention.
type Callback interface {
	Call(ctx context.Context, args ...any) ([]any, error)
}

// TypeTag classifies the shape of a value crossing the VM boundary. Each tag
// has a canonical Go type that Impls accept (for args) or return; codegen
// emits per-VM marshalling that produces or consumes that Go type.
type TypeTag int

// The TypeTag constants enumerate the parameter and return types a binding
// field or method can declare; TypeInvalid is the zero value.
const (
	TypeInvalid TypeTag = iota
	TypeString
	TypeInt
	TypeFloat
	TypeBool
	TypeStringSlice
	TypeStringMap
	TypeAnyMap
	TypeFunc
	TypeAny
	TypeBytes
	// TypeIndex is an int that names a position in a list. Buzz lists are
	// 0-based, matching the Go Impl, so the index needs no offset on the way in
	// or out (-1 means "not found"). The distinct tag is kept so a VM with a
	// different convention can be translated in one place if one is ever added.
	TypeIndex
)

// GoType returns the Go type name this tag maps to.
func (t TypeTag) GoType() string {
	switch t {
	case TypeString:
		return "string"
	case TypeInt, TypeIndex:
		return "int"
	case TypeFloat:
		return "float64"
	case TypeBool:
		return "bool"
	case TypeStringSlice:
		return "[]string"
	case TypeStringMap:
		return "map[string]string"
	case TypeAnyMap:
		return "map[string]any"
	case TypeFunc:
		return "Callback"
	case TypeAny:
		return "any"
	case TypeBytes:
		return "[]byte"
	default:
		return "<invalid>"
	}
}

// Arg is one positional parameter of a Method.
type Arg struct {
	Name     string
	Type     TypeTag
	Optional bool
	Variadic bool
	// Default is used when Optional is true and the caller omits the arg.
	// Must be of the Go type matching Type, or nil for "zero value".
	Default any
}

// Ret is one return value of a Method.
type Ret struct {
	Name string
	Type TypeTag
}

// Method declares one host function bound into the VM.
type Method struct {
	// Name is the canonical snake_case identifier (e.g. "read_file"); the Buzz
	// surface exposes it as camelCase derived from this (readFile).
	Name string
	// Doc is a one-line description used in generated .d.ts comments.
	Doc string
	// Args lists positional parameters in declaration order. Variadic, if
	// present, must be the last arg.
	Args []Arg
	// Returns lists return values. An error is always implicit on Impls
	// and surfaces as a Buzz runtime error; do not list it here.
	Returns []Ret
	// Impl is the typed Go function bound by this Method. Codegen reflects
	// over it to discover its package-qualified name and validates that its
	// signature matches Args + Returns + (error).
	Impl any
}

// Field is a static, table-level value on a Module. Unlike a Method, a Field
// is resolved once at registration time and stored as a plain value on the
// module's Buzz map — callers read it without function invocation (e.g.
// `vcs.name`, not `vcs.name()`).
type Field struct {
	Name string
	Doc  string
	Type TypeTag
	// Resolver is `func() (T, error)` or `func(context.Context) (T, error)`
	// where T matches Type. Called once per Session registration.
	Resolver any
}

// Module is a named collection of Fields + Methods imported under the module's
// bare name: after `import "fs"`, fs.glob; after `import "os"`, os.exec. magus
// layers these methods onto Buzz's own stdlib module of the same name.
type Module struct {
	Name    string
	Doc     string
	Fields  []Field
	Methods []Method
}

// ImplName returns the package-qualified function name of m.Impl, suitable
// for emitting into generated code (e.g. "host.FsGlob"). The leading import
// path is stripped; callers should ensure the emitted file imports this
// package as `host`.
func (m Method) ImplName() string {
	if m.Impl == nil {
		return ""
	}
	rv := reflect.ValueOf(m.Impl)
	if rv.Kind() != reflect.Func {
		return ""
	}
	full := runtime.FuncForPC(rv.Pointer()).Name()
	// full is like "github.com/.../host.FsGlob". Drop everything up to and
	// including the last "/", then drop the package name prefix.
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == '/' {
			full = full[i+1:]
			break
		}
	}
	// full is now "host.FsGlob" (or "host.FsGlob-fm" for method values).
	return full
}

// FuncName returns just the bare function name (e.g. "FsGlob") without the
// package qualifier. Useful for generating trampoline names.
func (m Method) FuncName() string {
	n := m.ImplName()
	for i := 0; i < len(n); i++ {
		if n[i] == '.' {
			return n[i+1:]
		}
	}
	return n
}

// FuncName returns the bare function name of f.Resolver (e.g. "VcsName").
func (f Field) FuncName() string {
	if f.Resolver == nil {
		return ""
	}
	rv := reflect.ValueOf(f.Resolver)
	full := runtime.FuncForPC(rv.Pointer()).Name()
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == '/' {
			full = full[i+1:]
			break
		}
	}
	for i := 0; i < len(full); i++ {
		if full[i] == '.' {
			return full[i+1:]
		}
	}
	return full
}

// ResolverTakesCtx reports whether f.Resolver's signature is
// (context.Context) (T, error) rather than () (T, error).
func (f Field) ResolverTakesCtx() bool {
	if f.Resolver == nil {
		return false
	}
	rt := reflect.TypeOf(f.Resolver)
	return rt.NumIn() == 1
}

var (
	mu      sync.Mutex
	modules = map[string]Module{}
)

// Register adds m to the global module registry. Called from each module's
// init() so magus-bindings-gen and the runtime registration paths can look up modules
// by name without an import loop.
func Register(m Module) {
	mu.Lock()
	defer mu.Unlock()
	if _, exists := modules[m.Name]; exists {
		panic(fmt.Sprintf("host: duplicate module registration: %q", m.Name))
	}
	if err := validateModule(m); err != nil {
		panic(fmt.Sprintf("host: module %q: %s", m.Name, err))
	}
	modules[m.Name] = m
}

// Get returns the Module registered under name, or false if unknown.
func Get(name string) (Module, bool) {
	mu.Lock()
	defer mu.Unlock()
	m, ok := modules[name]
	return m, ok
}

// All returns a snapshot of every registered Module, in unspecified order.
func All() []Module {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Module, 0, len(modules))
	for _, m := range modules {
		out = append(out, m)
	}
	return out
}

// validateModule checks each Method's Impl signature against its declared
// Args and Returns. Mismatches are programmer errors and panic at init.
func validateModule(m Module) error {
	for _, f := range m.Fields {
		if err := validateField(f); err != nil {
			return fmt.Errorf("field %q: %w", f.Name, err)
		}
	}
	for _, meth := range m.Methods {
		if err := validateMethod(meth); err != nil {
			return fmt.Errorf("method %q: %w", meth.Name, err)
		}
	}
	return nil
}

func validateField(f Field) error {
	if f.Resolver == nil {
		return fmt.Errorf("field Resolver must not be nil")
	}
	rt := reflect.TypeOf(f.Resolver)
	if rt.Kind() != reflect.Func {
		return fmt.Errorf("field Resolver must be a function, got %s", rt.Kind())
	}
	// Accept either () (T, error) or (context.Context) (T, error).
	switch rt.NumIn() {
	case 0:
	case 1:
		if rt.In(0).String() != "context.Context" {
			return fmt.Errorf("field Resolver single arg must be context.Context, got %s", rt.In(0))
		}
	default:
		return fmt.Errorf("field Resolver must take 0 or 1 args (ctx), got %d", rt.NumIn())
	}
	if rt.NumOut() != 2 {
		return fmt.Errorf("field Resolver must return (T, error), got %d returns", rt.NumOut())
	}
	if rt.Out(1).String() != "error" {
		return fmt.Errorf("field Resolver second return must be error, got %s", rt.Out(1))
	}
	return nil
}

func validateMethod(meth Method) error {
	if meth.Impl == nil {
		return fmt.Errorf("method Impl must not be nil")
	}
	rv := reflect.ValueOf(meth.Impl)
	rt := rv.Type()
	if rt.Kind() != reflect.Func {
		return fmt.Errorf("method Impl must be a function, got %s", rt.Kind())
	}

	// First param must be context.Context.
	if rt.NumIn() < 1 {
		return fmt.Errorf("method Impl must take context.Context as first arg")
	}
	if rt.In(0).String() != "context.Context" {
		return fmt.Errorf("method Impl first arg must be context.Context, got %s", rt.In(0))
	}

	// Compute expected number of declared args.
	wantArgs := 1 + len(meth.Args) // +1 for ctx
	hasVariadic := len(meth.Args) > 0 && meth.Args[len(meth.Args)-1].Variadic
	if hasVariadic {
		if !rt.IsVariadic() {
			return fmt.Errorf("declaration says variadic but Impl is not variadic")
		}
	} else if rt.IsVariadic() {
		return fmt.Errorf("method Impl is variadic but declaration has no variadic arg")
	}
	if rt.NumIn() != wantArgs {
		return fmt.Errorf("method Impl takes %d args, declaration has %d (incl. ctx)", rt.NumIn(), wantArgs)
	}

	// Last return must be error.
	if rt.NumOut() < 1 {
		return fmt.Errorf("method Impl must return error as last value")
	}
	if rt.Out(rt.NumOut()-1).String() != "error" {
		return fmt.Errorf("method Impl last return must be error, got %s", rt.Out(rt.NumOut()-1))
	}
	if rt.NumOut()-1 != len(meth.Returns) {
		return fmt.Errorf("method Impl has %d non-error returns, declaration has %d", rt.NumOut()-1, len(meth.Returns))
	}
	return nil
}
