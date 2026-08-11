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

// The module REGISTRY that binds these declarations to their generated trampolines is
// itself generated, from the WASM field below plus each module's Name and Path. It
// lives beside the trampolines rather than here because it names them.
//
//go:generate go run ../cmd/magus-utils moduleset -outdir ../internal/interp/bindings/gen

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
	// TypeFloatSlice is a list of numbers. Buzz has one numeric list, so an int
	// list and a double list are the same value crossing here; the Impl takes
	// []float64 and a caller that means integers rounds on the way out.
	TypeFloatSlice
	// TypeByteSlice is raw bytes, crossing as a Buzz list of ints rather than a
	// str. A Buzz string is rune-oriented, so arbitrary binary - a NUL, a 0xFF -
	// does not survive a round trip through one; anything that must move bytes
	// opaquely (an HMAC digest, a decoded base64 payload) uses this.
	TypeByteSlice
	// TypeStringSliceSlice is a list of string lists, e.g. csv.parse's rows of
	// fields.
	TypeStringSliceSlice
	TypeStringMap
	// TypeStringMapMap is a map of string maps, e.g. ini.parse's sections of
	// key-value pairs.
	TypeStringMapMap
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
	case TypeFloatSlice:
		return "[]float64"
	case TypeByteSlice:
		return "[]byte"
	case TypeStringSliceSlice:
		return "[][]string"
	case TypeStringMapMap:
		return "map[string]map[string]string"
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
	// Enum names the Buzz enum this argument's string must be a case of, for an arg
	// whose Type is TypeString but whose values are a closed set. Like Object it
	// changes only the DECLARED signature: an enum case is a string at runtime, so
	// the Impl still receives a string and needs no decoder.
	//
	// The name must be a boundaryEnums entry in cmd/magus-utils. Without it a
	// closed-set parameter reads as `str`, and naming an algorithm magus does not
	// implement is a runtime throw rather than something the checker catches.
	Enum string
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
	// Enum names the Buzz enum this return's string is a case of. Same role as
	// Arg.Enum, on the way out.
	Enum string
}

// Namespace is a group of related host functions a module exposes behind one of its
// members: `magus\cache.remote(...)`, `magus\secret.read(...)`.
//
// It is NOT a nested module. Buzz has no nested namespace - ast.NamespaceStmt carries
// a single name - and the call site says so: `magus\cache.remote(x)` is a backslash
// (namespace access) followed by a DOT (member access on a value). So the group is an
// OBJECT held by the module, and its functions are that object's static methods. This
// is upstream's own shape, the one `io\File.open(...)` takes.
//
// Declaring it buys the checking a bare value cannot: an object reports an unknown
// member, so a typo in `magus\cache.remote` is a load error instead of a null that
// reaches the VM. The runtime binding does not change - internal/interp/bindings still
// MapSets a map under this name, and a Buzz object IS a map at run time.
type Namespace struct {
	Name string
	Doc  string
	// Methods are all Extern by construction: the whole point of a Namespace is that
	// the runtime assembles it, so there is no Impl to generate a trampoline from.
	// Validation enforces it rather than trusting the author.
	Methods []Method
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
	// Raises is authored, not derived: true when Impl can return a non-nil error
	// to the Buzz caller under real inputs (not merely when its signature ends in
	// `error` - many Impls declare one and never return it non-nil). moduledecls
	// reads this to emit `!> any` on the generated extern declaration, which is
	// what makes the checker's propagate-or-catch enforcement (BZZ1006) apply to
	// host calls. Get this wrong and either a caller loses a real error path
	// unchecked (false), or every call site needs a needless try/catch (true).
	Raises bool
	// Impl is the typed Go function bound by this Method. Codegen reflects
	// over it to discover its package-qualified name and validates that its
	// signature matches Args + Returns + (error). Nil only when Extern is set.
	Impl any
	// Extern marks a member DECLARED here but BOUND ELSEWHERE - today by
	// internal/interp/bindings, which MapSets it onto the module's namespace value
	// at run time. It carries no Impl and gets no generated trampoline; what it
	// gets is a declaration.
	//
	// The name is upstream Buzz's: `export extern fun` is exactly this, a signature
	// whose implementation the host binds. The generated decls are already extern
	// declarations, so this only feeds entries into a mechanism that existed.
	//
	// It exists because a descriptor could previously only mean "declared AND
	// implemented statically", so any member needing a dynamic binding - one that
	// closes over per-Open state, or that binds differently per surface, like
	// `magus\project` being the real thing in a magusfile and an MGS1022 guard in a
	// script - had to be MapSet and was therefore invisible to the checker. That is
	// how `crypto\hash`, a function the crypto module has never had, type-checked
	// inside a spell's AWS SigV4 signing path.
	//
	// Set it only when something really does bind the member at run time. An Extern
	// with nothing behind it declares a member that does not exist, which is the
	// same defect pointed the other way.
	Extern bool
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
// bare name: after `import "fs"`, fs.glob; after `import "os"`, proc.exec. magus
// layers these methods onto Buzz's own stdlib module of the same name.
type Module struct {
	// Name is the identifier a magusfile calls the module by: `json` in
	// `json\parse(...)`. It is also the key for everything derived from the
	// module - the generated trampoline's Go function name, its file name, the
	// Modules registry key - so it must stay a legal identifier.
	Name string
	// Path is the import spelling, when it differs from Name: `encoding/json` for
	// the module bound as `json`. Empty means the two are the same, which is the
	// case for every top-level module.
	//
	// The split exists because Buzz binds a slash-path import under its BASENAME
	// (see gopherbuzz/session.go's resolveImport, the same rule `magus/spell/go`
	// already follows). So the path is only the spelling of the import line; the
	// name is what every call site actually writes, and what codegen has to be
	// able to put in a Go identifier. Conflating them would mean a module called
	// `encoding/json` whose generated function was RegisterEncoding/json.
	//
	// Two modules may not share a Name even under different paths: Register
	// panics on the collision, which is correct rather than incidental, because a
	// magusfile cannot import both unaliased anyway.
	Path    string
	Doc     string
	Fields  []Field
	Methods []Method
	// Namespaces are member groups the runtime assembles; see Namespace.
	Namespaces []Namespace
	// WASM marks a module the browser playground can install: pure compute, with no
	// process, filesystem, or network access. It is what
	// internal/interp/bindings/gen's WASM capability is generated FROM.
	//
	// Declared here because it is a fact about the MODULE, and it had no home: the
	// classification lived only as prose in doc comments ("Pure computation:
	// WASM-safe", std/math.go) and as a hand-maintained table in bindings/gen, whose
	// wasm half carried the instruction "Keep this in sync with the WASM-capable
	// entries of modules.go" - a mirror that could drift and a rule no test could
	// check, because nothing upstream declared the answer.
	//
	// uuid is WASM despite generating randomness: the browser supplies
	// getRandomValues. The test is whether the BROWSER can provide it, not whether
	// the operation sounds pure.
	WASM bool
}

// ImportPath returns the path a magusfile imports this module by: Path when it
// declares one, otherwise Name.
func (m Module) ImportPath() string {
	if m.Path != "" {
		return m.Path
	}
	return m.Name
}

var (
	mu      sync.Mutex
	modules = map[string]Module{}
)

// Register adds m to the global module registry. Called from each module's
// init() so magus-utils bindings and the runtime registration paths can look up modules
// by name without an import loop.
//
// std/encoding's nine leaf packages do NOT call this: std/encoding imports std
// for the Module vocabulary, so std registering them back here would import
// std/encoding in turn and cycle. They are collected explicitly instead (see
// std/encoding/register.go) and unioned with this registry one layer above std,
// in internal/hostmodules - the only place that imports both.
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

// ValidateModule checks m's Fields and Methods against their declared Impls,
// the same check Register runs before storing a module. std/encoding's leaf
// packages call it directly (see std/encoding/register.go) because they
// cannot call Register itself without cycling back through std - see
// Register's doc for why - but a malformed descriptor should still fail fast
// at the same place in the program's life as every other module's, not
// silently reach codegen as bad data.
func ValidateModule(m Module) error {
	return validateModule(m)
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
