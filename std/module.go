// Package std is the single source of truth for host-binding APIs that
// magusfiles call into. Each module (os, fs, vcs, …) declares its
// Methods here as a Module value with typed args, return types, and a Go
// Impl. The magus-utils bindings tool consumes these declarations and emits the
// Buzz trampolines into internal/interp/bindings/gen from the same Impl.
package std

import (
	"context"
	"fmt"
	"sync"
)

// Callback is the host-side handle for a VM-side function value passed as
// an argument. The generated bindings layer wraps a buzz.Session + function value.
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
	// Object names the Buzz object this argument's map must match, for an arg whose
	// Type is TypeAnyMap but whose shape is a declared boundary type. It changes only
	// the DECLARED signature the checker reads: a Buzz object is a map at runtime, so
	// the Impl still receives map[string]any and needs no decoder.
	//
	// Without it an argument like encoding.build_url's `parts` reads as {str: any} even
	// though parse_url returns a URL - so the round trip was typed in one direction and
	// untyped in the other, and a caller could pass any map at all.
	Object string
}

// Ret is one return value of a Method.
type Ret struct {
	Name string
	Type TypeTag
	// Object names the Buzz object this return marshals to, for a method whose
	// Impl returns a Go struct carrying BuzzObject (or a slice of them). Empty for a
	// scalar return.
	//
	// It is documentation the CHECKER and the reader can both use, not a
	// marshalling instruction: the generator already recognizes an object by
	// reflecting on the Impl, so the bytes are correct either way. What was
	// missing is the NAME. Without it a method's return types as {str: any}
	// everywhere outside the generator, so `magus\cmd(...)` hands back a map
	// whose field names nothing checks - and a magusfile author has no way to
	// learn that annotating `> ExecResult` would make the checker verify them.
	//
	// The generator VALIDATES this against the reflected Impl and fails codegen on
	// a mismatch or an omission, so it cannot drift from the struct it names. That
	// is the whole reason it is safe to state twice.
	Object string
}

// Method declares one host function bound into the VM.
type Method struct {
	// Name is the canonical snake_case identifier (e.g. "read_file"); the Buzz
	// surface exposes it as camelCase derived from this (readFile).
	Name string
	// BuzzName, when non-empty, is the verbatim Buzz-surface name, overriding the
	// camelCase derivation from Name. The magus DSL keeps a few snake_case
	// primitives (has_charm) that magusfiles and the static charm extractor match
	// by literal name; those set BuzzName so codegen doesn't rewrite them.
	BuzzName string
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

// Field is a static, table-level value on a Module: resolved once at registration
// and stored as a plain value on the module's Buzz map, so a caller reads it without
// invocation.
//
// NO MODULE USES THIS, and TestNoModuleDeclaresFields keeps it that way. A Field
// generates no extern declaration - Buzz has syntax for `extern fun` and none for an
// extern value - so the checker cannot type it, and a caller who writes the parens
// gets a runtime "str is not callable" instead of a compile error. vcs.name and
// vcs.base were Fields and cost exactly that; both are Methods now. Declare a
// constant as a Method returning it.
//
// The type stays because magus-docs, langservice-manifest and the ModuleFieldEntry
// boundary type all render Fields, and dropping it would change a Buzz-visible
// introspection shape to delete a branch that already never runs.
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

var (
	mu      sync.Mutex
	modules = map[string]Module{}
)

// Register adds m to the global module registry. Called from each module's
// init() so magus-utils bindings and the runtime registration paths can look up modules
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

// validateModule (and its validateField/validateMethod helpers) reflect over
// each Impl's signature to catch declaration/Impl mismatches at init. They live
// in validate.go (native) with a no-op stub in validate_wasm.go, because TinyGo's
// wasm reflect omits (reflect.Type).NumIn/NumOut and would panic at startup. The
// check is a programmer-error guard that has already run on the host by the time a
// wasm build exists, so skipping it in the browser is safe.
