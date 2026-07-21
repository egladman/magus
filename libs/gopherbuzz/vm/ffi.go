package vm

// FFI: the zdef() builtin and its platform boundary.
//
// zdef(libname, cdecl) loads a shared library and binds the C functions
// declared in cdecl, returning a Buzz map of name -> direct callable:
//
//	const lib = zdef("libm", "double sqrt(double x);");
//	const r = lib.sqrt(4.0);
//
// Architecture portability
// ────────────────────────
// Parsing the C-declaration subset is pure Go and compiles everywhere (it is in
// this file). Actually *binding* a C symbol — opening a shared object and
// building an ABI-correct call stub — is inherently platform-specific and is
// delegated to an FFIProvider. The default provider (ffi_purego.go) uses
// github.com/ebitengine/purego and is built only on the OS/arch combinations
// purego supports; on every other target no provider is registered and zdef()
// returns a clear "unsupported" error instead of failing to compile. The rest of
// the interpreter (VM, parser, objects, fibers, …) is fully portable, so Buzz
// runs anywhere Go runs — only zdef() is gated.
//
// Embedding FFI on an unsupported platform
// ────────────────────────────────────────
// FFIProvider is exported so an embedder targeting a niche platform purego does
// not cover can supply their own binding implementation and install it with
// RegisterFFIProvider before running any Buzz code. They receive the already-
// parsed C signatures (CFuncSig) and return the name->callable map; the C-decl
// parser, the Buzz value constructors (DirectValue, NewMap), and the type model
// (CType/CParam/CFuncSig) are all exported for that purpose. Our own purego
// backend is just the default implementation of this same interface.

import (
	"context"
	"fmt"
	"strings"
)

// CType is a C type from the zdef() declaration subset, mapped to a Buzz value
// kind at the call boundary. It is the element of the FFI type model shared
// between the C-decl parser and an FFIProvider.
type CType uint8

const (
	CVoid    CType = iota
	CBool          // _Bool / bool
	CInt           // signed integer (int, long, longlong, intN_t, size_t)
	CUint          // unsigned integer
	CFloat         // float (32-bit)
	CDouble        // double (64-bit)
	CCharPtr       // char* / const char* → Buzz str
	CVoidPtr       // void* → Buzz int (raw address)
	CAddr          // extern variable of opaque type → the symbol's own address
	CPoint2D       // CGPoint/NSPoint returned by value → Buzz map {x, y}
	CRect4D        // CGRect/NSRect returned by value → Buzz map {x, y, w, h}
	CUnsupported
)

// CParam is one parameter of a C function signature.
type CParam struct {
	Name string
	Type CType
}

// CFuncSig is a parsed C declaration: a function prototype (its name, return
// type, and parameters) or, when IsVar is set, an `extern` variable
// declaration. ParseCDecls produces these; an FFIProvider binds them.
//
// For a variable, Ret carries the load kind: a pointer type loads the pointer
// stored at the symbol (CCharPtr additionally follows it to a Buzz str), a
// scalar type loads a value of that width (VarTypeName preserves the exact C
// token so the provider can size it), and CAddr — any type the parser does not
// model, e.g. a struct — binds the symbol's own address, which is what C code
// passing &someGlobal needs.
type CFuncSig struct {
	Name        string
	Ret         CType
	Params      []CParam
	IsVar       bool
	VarTypeName string
	// IsStruct marks a Zig `const Name = extern struct {…}` declaration: not a
	// symbol at all, but a layout. The provider computes its C layout (a Zig
	// extern struct's layout IS the C layout, by definition) and binds the
	// name to a {size, align, offsets} map, so scripts can ffi.alloc and fill
	// it by reference — upstream's struct semantics ("always by reference").
	IsStruct       bool
	FieldTypeNames []string
}

// FFIProvider binds parsed C function signatures from a shared library into
// callable Buzz values. It is the platform boundary of zdef(): parsing the C
// declarations is portable and done before the provider is called, so an
// implementation only has to (1) open the named library and (2) produce, for
// each signature, an ABI-correct direct callable. Implementations must be safe
// for use by one Session at a time (the same single-goroutine ownership the rest
// of the interpreter assumes).
//
// The default implementation lives in ffi_purego.go (built on purego-supported
// platforms). Embedders on other platforms implement this and install it with
// RegisterFFIProvider; they can build the result map with NewMap/MapSet and each
// entry with DirectValue.
type FFIProvider interface {
	// OpenLibrary resolves libname and binds each signature in sigs, returning a
	// Buzz map Value of function name -> direct callable. It is called by zdef()
	// after the cdecl string has been parsed.
	OpenLibrary(libname string, sigs []CFuncSig) (Value, error)
}

// ffiProvider is the installed FFI backend, or nil when none is registered (the
// platform has no provider). Set at init() by the default purego backend, or by
// an embedder via RegisterFFIProvider. Single-threaded by the same ownership
// model as the rest of the package; not guarded by a lock.
var ffiProvider FFIProvider

// RegisterFFIProvider installs p as the FFI backend used by zdef(). It is
// intended for embedders adding FFI on a platform the default purego backend
// does not support; call it once during setup, before running Buzz code. A nil p
// is ignored. The default backend registers itself at init() on supported
// platforms, so calling this overrides that default if both are present.
func RegisterFFIProvider(p FFIProvider) {
	if p != nil {
		ffiProvider = p
	}
}

// GetFFIProvider returns the currently installed FFI provider, or nil if none
// is registered. Intended for tests that need to save and restore the provider.
func GetFFIProvider() FFIProvider { return ffiProvider }

// SetFFIProvider sets the FFI provider unconditionally (unlike RegisterFFIProvider,
// it accepts nil). Intended for tests that need to temporarily replace or clear it.
func SetFFIProvider(p FFIProvider) { ffiProvider = p }

// callbackMaker builds a C-callable function pointer that re-enters Buzz, or is
// nil on platforms with no purego backend. Set by ffi_purego.go's init(); see
// MakeCallback. Kept as a function value (not a method on FFIProvider) because it
// is a leaf capability of the default backend, not something an embedder must
// reimplement to get zdef() working.
var callbackMaker func(ctx context.Context, fn Value, ret CType, params []CType) (uintptr, error)

// MakeCallback wraps a Buzz function as a C function pointer (returned as its
// machine address) so it can be passed where C expects a callback — e.g. the
// comparator of qsort(). retName/paramNames are C type names from the zdef
// subset. Floating types are rejected: purego's callback ABI carries only
// integer/pointer/bool arguments and a single integer/pointer/bool (or void)
// result, which covers comparators, predicates, and visitors.
//
// The returned pointer is passed to C as a void* zdef parameter. When C invokes
// it, the wrapper marshals the C arguments to Buzz values, runs the Buzz function
// on a nested VM, and marshals its result back. A C callback has nowhere to
// propagate a Buzz error, so a raised error yields the zero return value.
func MakeCallback(ctx context.Context, fn Value, retName string, paramNames []string) (uintptr, error) {
	if !fn.IsFun() {
		return 0, fmt.Errorf("buzz: ffi: callback target must be a function, got %s", fn.buzzKind())
	}
	ret, err := callbackCType(retName, true)
	if err != nil {
		return 0, err
	}
	params := make([]CType, len(paramNames))
	for i, p := range paramNames {
		ct, err := callbackCType(p, false)
		if err != nil {
			return 0, err
		}
		params[i] = ct
	}
	if callbackMaker == nil {
		return 0, fmt.Errorf("buzz: ffi: callbacks are not supported on this platform (no FFI provider registered)")
	}
	return callbackMaker(ctx, fn, ret, params)
}

// callbackCType resolves a C type name for a callback signature, rejecting
// floating types (unsupported by the callback ABI) and, for a parameter, void.
func callbackCType(name string, isRet bool) (CType, error) {
	if isRet && strings.TrimSpace(name) == "void" {
		return CVoid, nil
	}
	ct := parseCType(name)
	if ct == CUnsupported {
		ct = zigTypeToCType(name) // accept the Zig dialect's spellings too
	}
	if ct == CUnsupported {
		// A trailing '*' (any pointer) is a valid integer-width address.
		if strings.HasSuffix(strings.TrimSpace(name), "*") {
			return CVoidPtr, nil
		}
		return CUnsupported, fmt.Errorf("buzz: ffi: unsupported callback type %q", name)
	}
	if ct == CFloat || ct == CDouble {
		return CUnsupported, fmt.Errorf("buzz: ffi: callbacks cannot use floating type %q (the callback ABI carries integer/pointer values only)", name)
	}
	return ct, nil
}

// ---- C-declaration subset parser (portable) ----
//
// Supported C types: void, bool, int, uint, short, ushort, long, ulong,
// longlong, ulonglong, float, double, char*, const char*, void*, intN_t,
// uintN_t, size_t.

// parseCType maps a C type token to its CType.
func parseCType(tok string) CType {
	tok = strings.TrimPrefix(tok, "const ")
	tok = strings.TrimSpace(tok)
	switch tok {
	case "void":
		return CVoid
	case "bool", "_Bool":
		return CBool
	case "int", "signed", "signed int",
		"short", "short int", "signed short",
		"long", "long int", "signed long",
		"long long", "long long int",
		"int8_t", "int16_t", "int32_t", "int64_t",
		"ssize_t", "ptrdiff_t":
		return CInt
	case "unsigned", "unsigned int",
		"unsigned short", "unsigned short int",
		"unsigned long", "unsigned long int",
		"unsigned long long", "unsigned long long int",
		"uint8_t", "uint16_t", "uint32_t", "uint64_t",
		"size_t":
		return CUint
	case "float":
		return CFloat
	case "double":
		return CDouble
	case "char *", "char*", "const char *", "const char*":
		return CCharPtr
	case "void *", "void*":
		return CVoidPtr
	case "CGPoint", "NSPoint", "CGSize", "NSSize":
		// A struct of two doubles, by value. Supported in return position
		// (registers on amd64/arm64); as a parameter, pass two doubles or a
		// pointer instead — parseCParam rejects it with that advice.
		return CPoint2D
	case "CGRect", "NSRect":
		// Four doubles by value (origin + size), e.g. CGDisplayBounds. Return
		// only: registers on arm64 (HFA), hidden sret pointer on amd64 —
		// purego builds both.
		return CRect4D
	default:
		return CUnsupported
	}
}

// ParseCDecls parses one or more C function prototypes separated by semicolons
// into signatures an FFIProvider can bind. Exported so an embedder implementing
// FFIProvider can reuse Buzz's C-decl subset rather than reinventing it.
func ParseCDecls(src string) ([]CFuncSig, error) {
	sigs := make([]CFuncSig, 0, strings.Count(src, ";")+1)
	for _, part := range strings.Split(src, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		sig, err := parseSingleCDecl(part)
		if err != nil {
			return nil, err
		}
		sigs = append(sigs, sig)
	}
	return sigs, nil
}

func parseSingleCDecl(src string) (CFuncSig, error) {
	lp := strings.Index(src, "(")
	if lp < 0 {
		if rest, ok := strings.CutPrefix(src, "extern "); ok {
			return parseExternVar(strings.TrimSpace(rest))
		}
		return CFuncSig{}, fmt.Errorf("buzz: ffi: not a function prototype: %q (declare a data symbol as \"extern <type> name;\")", src)
	}
	rp := strings.LastIndex(src, ")")
	if rp < 0 || rp < lp {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: missing ')' in prototype: %q", src)
	}

	head := strings.TrimSpace(src[:lp])
	lastSpace := strings.LastIndexAny(head, " \t*")
	if lastSpace < 0 {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: cannot parse return type and name from %q", head)
	}
	funcName := strings.TrimSpace(head[lastSpace+1:])
	retStr := strings.TrimSpace(head[:lastSpace+1])
	retStr = strings.TrimRight(retStr, "* \t")

	var retType CType
	fullRet := strings.TrimSpace(src[:lp])
	if strings.Contains(fullRet, "char *") || strings.Contains(fullRet, "char*") {
		retType = CCharPtr
	} else if strings.Contains(fullRet, "*") {
		// Any other pointer return (void*, int*, struct Foo*, …) is an opaque
		// machine address. We surface it to Buzz as an int the script can hand to
		// ffi.read*/ffi.write* or pass back into another C call.
		retType = CVoidPtr
	} else {
		retType = parseCType(retStr)
		if retType == CUnsupported {
			return CFuncSig{}, fmt.Errorf("buzz: ffi: unsupported return type %q in %q", retStr, src)
		}
	}

	paramStr := strings.TrimSpace(src[lp+1 : rp])
	var params []CParam
	if paramStr != "" && paramStr != "void" {
		for _, p := range strings.Split(paramStr, ",") {
			p = strings.TrimSpace(p)
			if p == "" || p == "..." {
				continue
			}
			cp, err := parseCParam(p)
			if err != nil {
				return CFuncSig{}, fmt.Errorf("buzz: ffi: %w in prototype %q", err, src)
			}
			params = append(params, cp)
		}
	}

	return CFuncSig{Name: funcName, Ret: retType, Params: params}, nil
}

// parseExternVar parses the body of an `extern <type> name` variable
// declaration (the "extern " prefix already removed). The declared type picks
// the binding mode documented on CFuncSig: pointer → load, known scalar →
// load by width, anything else (struct, unknown typedef) → the symbol address.
func parseExternVar(src string) (CFuncSig, error) {
	fields := strings.Fields(src)
	if len(fields) < 2 {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: extern declaration needs a type and a name: %q", src)
	}
	name := strings.TrimLeft(fields[len(fields)-1], "*")
	if name == "" {
		return CFuncSig{}, fmt.Errorf("buzz: ffi: extern declaration has no name: %q", src)
	}
	typeTok := strings.TrimSpace(strings.TrimSuffix(strings.Join(fields[:len(fields)-1], " "), "*"))
	// A '*' attached to the name ("void *kFoo") belongs to the type.
	starred := strings.Contains(src, "*")

	kind := CAddr
	switch {
	case starred && (strings.Contains(src, "char *") || strings.Contains(src, "char*")):
		kind = CCharPtr
	case starred:
		kind = CVoidPtr
	default:
		if k := parseCType(typeTok); k != CUnsupported && k != CVoid {
			kind = k
		}
	}
	return CFuncSig{Name: name, Ret: kind, IsVar: true, VarTypeName: typeTok}, nil
}

func parseCParam(s string) (CParam, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "char *") || strings.Contains(s, "char*") {
		name := extractParamName(s)
		return CParam{Name: name, Type: CCharPtr}, nil
	}
	// Every other pointer parameter (void*, int*, double*, struct Foo*, …) is an
	// opaque address. The script passes an int address — typically one returned
	// by ffi.alloc — and reads results back with ffi.read*. Mapping all of these
	// to CVoidPtr (rather than stripping the '*' and binding the pointee scalar
	// directly, which silently passed a value where C expected an address) is
	// what makes out-parameters and by-reference structs work.
	if strings.Contains(s, "*") {
		name := extractParamName(s)
		return CParam{Name: name, Type: CVoidPtr}, nil
	}
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return CParam{}, fmt.Errorf("empty parameter")
	}
	var typeTok string
	var name string
	if len(parts) == 1 {
		typeTok = parts[0]
		name = ""
	} else {
		name = strings.TrimLeft(parts[len(parts)-1], "*")
		typeTok = strings.Join(parts[:len(parts)-1], " ")
		typeTok = strings.TrimRight(typeTok, "* \t")
	}
	k := parseCType(typeTok)
	if k == CUnsupported {
		return CParam{}, fmt.Errorf("unsupported type %q", typeTok)
	}
	if k == CPoint2D || k == CRect4D {
		return CParam{}, fmt.Errorf("by-value struct parameter %q not supported: declare separate double parameters or a pointer", typeTok)
	}
	return CParam{Name: name, Type: k}, nil
}

func extractParamName(s string) string {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return ""
	}
	last := parts[len(parts)-1]
	return strings.TrimLeft(last, "*")
}

// FFIDeclNames returns the names declared by a zdef cdecl string (functions,
// extern vars, and structs), sniffing the Zig vs C dialect the same way
// builtinZdef does. It is used by the compiler to lower a top-level
// `zdef("lib", "<decls>")` statement into free-function declarations (upstream
// Buzz's zdef semantics), so the declared symbols become callable by bare name.
// Returns nil on a parse error — the runtime zdef() call surfaces the diagnostic.
func FFIDeclNames(cdecl string) []string {
	var sigs []CFuncSig
	var err error
	if looksLikeZigDecls(cdecl) {
		sigs, err = ParseZigDecls(cdecl)
	} else {
		sigs, err = ParseCDecls(cdecl)
	}
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(sigs))
	for _, s := range sigs {
		names = append(names, s.Name)
	}
	return names
}

// builtinZdef is the Buzz `zdef(libname, cdecl)` built-in. It parses the C
// declarations (portable) and hands the signatures to the installed FFIProvider
// (platform-specific). When no provider is registered — a platform purego does
// not support and where the embedder installed none — it returns a clear error
// rather than panicking or silently no-oping.
func builtinZdef(_ context.Context, args []Value) (Value, error) {
	if len(args) < 2 {
		return Null, fmt.Errorf("buzz: zdef() requires (libname, cdecl) arguments")
	}
	if args[0].tag() != tagStr {
		return Null, fmt.Errorf("buzz: zdef() libname must be str, got %s", args[0].buzzKind())
	}
	if args[1].tag() != tagStr {
		return Null, fmt.Errorf("buzz: zdef() cdecl must be str, got %s", args[1].buzzKind())
	}
	if ffiProvider == nil {
		return Null, fmt.Errorf("buzz: zdef() FFI is not supported on this platform " +
			"(no FFI provider registered); see buzz.RegisterFFIProvider")
	}
	libName := args[0].asStr().V
	cdecl := args[1].asStr().V

	// Two declaration dialects, one type model: upstream-Buzz Zig
	// declarations (fn add(a: c_int) c_int;) and C prototypes. Sniffed per
	// call, so both styles coexist — see ffi_zig.go.
	var sigs []CFuncSig
	var err error
	if looksLikeZigDecls(cdecl) {
		sigs, err = ParseZigDecls(cdecl)
	} else {
		sigs, err = ParseCDecls(cdecl)
	}
	if err != nil {
		return Null, err
	}
	return ffiProvider.OpenLibrary(libName, sigs)
}
