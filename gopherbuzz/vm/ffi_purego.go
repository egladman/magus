// The build constraint matches github.com/ebitengine/purego's own support
// matrix for Dlopen/Dlsym/RegisterFunc (its syscall_sysv.go and func.go tags):
// the OS/arch combinations where purego can open a shared library and build an
// ABI-correct call stub. On anything else this file is excluded and zdef()
// reports that FFI is unsupported (see ffi.go). Keep this tag in sync with
// purego on upgrades; a mismatch surfaces immediately as a build failure on the
// affected target, never as a runtime fault.
//
// Note the !android clause: in Go's build system GOOS=android also satisfies the
// `linux` constraint, but purego's pure-Go dlopen path excludes android (its
// android backend needs cgo), so we exclude it here too — matching purego's own
// `&& !android` on func.go.
//
//go:build (darwin || freebsd || netbsd || (linux && (386 || amd64 || arm || arm64 || loong64 || ppc64le || riscv64 || (cgo && s390x)))) && !android

package vm

// Default FFIProvider: runtime dynamic linking via purego.
//
// purego.RegisterFunc builds an architecture-correct calling-convention stub
// (e.g. XMM registers for float/double on amd64) so we can call C functions
// without cgo. This file is the platform-specific half of zdef(); the portable
// half (C-decl parsing, the FFIProvider interface, the builtin) is in ffi.go.

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"unsafe"

	"github.com/ebitengine/purego"
)

// init registers the purego backend as the default FFI provider on supported
// platforms. An embedder can still override it via RegisterFFIProvider. It also
// wires the callback maker used by MakeCallback (ffi.go), since building a C
// function pointer needs purego.NewCallback.
func init() {
	RegisterFFIProvider(puregoFFI{})
	callbackMaker = puregoMakeCallback
}

// puregoMakeCallback builds a C function pointer (purego.NewCallback) whose body
// re-enters Buzz to run fn. The C ABI restricts callbacks to integer/pointer/bool
// arguments and a single such result (or void) — MakeCallback already rejected
// floating types — so the reflect signature here uses only those kinds.
func puregoMakeCallback(ctx context.Context, fn Value, ret CType, params []CType) (uintptr, error) {
	paramTypes := make([]reflect.Type, len(params))
	for i, p := range params {
		paramTypes[i] = cTypeToReflect(p)
	}
	var retTypes []reflect.Type
	if ret != CVoid {
		retTypes = []reflect.Type{cTypeToReflect(ret)}
	}
	fnType := reflect.FuncOf(paramTypes, retTypes, false)

	trampoline := reflect.MakeFunc(fnType, func(in []reflect.Value) []reflect.Value {
		bargs := make([]Value, len(params))
		for i, p := range params {
			bargs[i] = reflectArgToBuzz(in[i], p)
		}
		// Run the Buzz callback on a fresh VM (the global heap makes fn portable
		// across VMs). A C callback cannot receive a Buzz error, so on failure we
		// fall through to the zero return value.
		result := Null
		nv := NewVM(ctx)
		if err := nv.Call(fn, bargs); err == nil {
			if r, e := nv.Exec(); e == nil {
				result = r
			}
		}
		if ret == CVoid {
			return nil
		}
		return []reflect.Value{buzzRetToReflect(result, ret, fnType.Out(0))}
	})

	return purego.NewCallback(trampoline.Interface()), nil
}

// reflectArgToBuzz converts one C argument delivered to a callback into the Buzz
// value the script's function receives. It is the inverse of buzzToReflectArg for
// the callback-legal kinds.
func reflectArgToBuzz(v reflect.Value, kind CType) Value {
	switch kind {
	case CBool:
		return BoolValue(v.Bool())
	case CInt:
		return IntValue(v.Int())
	case CUint:
		return IntValue(int64(v.Uint()))
	case CCharPtr:
		return StrValue(v.String())
	case CVoidPtr:
		return UDValue(uintptr(v.Uint())) // foreign pointer (ud), full 64-bit
	default:
		return Null
	}
}

// buzzRetToReflect converts the Buzz callback's result back into the C return
// type, as a reflect.Value of exactly outType (what RegisterFunc/NewCallback
// expects). Out-of-type or null results collapse to the zero value.
func buzzRetToReflect(v Value, kind CType, outType reflect.Type) reflect.Value {
	switch kind {
	case CBool:
		return reflect.ValueOf(v.Bool())
	case CInt:
		if v.IsInt() {
			return reflect.ValueOf(v.AsInt())
		}
	case CUint:
		if v.IsInt() {
			return reflect.ValueOf(uint64(v.AsInt()))
		}
	case CVoidPtr:
		if v.IsUD() {
			return reflect.ValueOf(v.AsUD())
		}
		if v.IsInt() {
			return reflect.ValueOf(uintptr(v.AsInt()))
		}
	}
	return reflect.Zero(outType)
}

// puregoFFI implements FFIProvider using purego's dlopen/dlsym + RegisterFunc.
type puregoFFI struct{}

// OpenLibrary opens libname and binds each signature, returning a Buzz map of
// function name -> direct callable. An `extern` variable declaration binds as
// a plain value instead: its symbol is resolved and read once at zdef() time
// (these are load-time constants like kCFBooleanTrue, not live cells).
func (puregoFFI) OpenLibrary(libname string, sigs []CFuncSig) (Value, error) {
	handle, err := openLib(libname)
	if err != nil {
		return Null, err
	}
	m := newMapObj()
	for _, sig := range sigs {
		if sig.IsStruct {
			// A struct declaration is a layout, not a symbol: bind its name to
			// {size, align, offsets} so scripts can ffi.alloc and fill it by
			// reference. The Zig extern-struct layout is the C layout, and the
			// portable layout engine computes it (Zig type spellings included).
			size, align, offsets, err := StructLayout(sig.FieldTypeNames)
			if err != nil {
				return Null, fmt.Errorf("buzz: ffi: struct %s: %w", sig.Name, err)
			}
			lay := newMapObj()
			lay.set("size", IntValue(int64(size)))
			lay.set("align", IntValue(int64(align)))
			items := make([]Value, len(offsets))
			for i, off := range offsets {
				items[i] = IntValue(int64(off))
			}
			lay.set("offsets", ListValue(items))
			m.set(sig.Name, heapValue(tagMap, lay))
			continue
		}
		sym, err := purego.Dlsym(handle, sig.Name)
		if err != nil {
			return Null, fmt.Errorf("buzz: ffi: symbol %q not found in %q: %w", sig.Name, libname, err)
		}
		if sig.IsVar {
			v, err := loadExternVar(sig, sym)
			if err != nil {
				return Null, fmt.Errorf("buzz: ffi: extern %q in %q: %w", sig.Name, libname, err)
			}
			m.set(sig.Name, v)
			continue
		}
		fn, err := buildFFIFunc(sig, sym)
		if err != nil {
			return Null, err
		}
		m.set(sig.Name, fn)
	}
	return heapValue(tagMap, m), nil
}

// foreignPtr converts a C address to unsafe.Pointer for a read. The address
// came from dlsym (or a pointer loaded through one), so it never points into
// the Go heap — the aliasing rules vet's unsafeptr check guards against don't
// apply. The &addr round-trip is the conventional conversion vet accepts.
func foreignPtr(addr uintptr) unsafe.Pointer {
	//nolint:gosec // G103: audited — addr is a dlsym-resolved C address, never a Go pointer.
	return *(*unsafe.Pointer)(unsafe.Pointer(&addr))
}

// loadExternVar materializes an extern data symbol as a Buzz value. sym is the
// address dlsym resolved — the address OF the variable — so pointer-typed
// declarations load the pointer stored there, scalar declarations load a value
// of the declared width, and CAddr (opaque/struct types) yields sym itself,
// which is what a C parameter written &someGlobal wants.
func loadExternVar(sig CFuncSig, sym uintptr) (Value, error) {
	switch sig.Ret {
	case CAddr:
		// A pointer (the symbol's own address) → ud, full 64-bit.
		return UDValue(sym), nil
	case CVoidPtr:
		return UDValue(*(*uintptr)(foreignPtr(sym))), nil
	case CCharPtr:
		p := *(*uintptr)(foreignPtr(sym))
		if p == 0 {
			return StrValue(""), nil
		}
		return StrValue(goCString(p)), nil
	case CBool:
		return BoolValue(*(*byte)(foreignPtr(sym)) != 0), nil
	case CFloat:
		return FloatValue(float64(*(*float32)(foreignPtr(sym)))), nil
	case CDouble:
		return FloatValue(*(*float64)(foreignPtr(sym))), nil
	case CInt, CUint:
		size, _, ok := CTypeLayout(sig.VarTypeName)
		if !ok {
			return Null, fmt.Errorf("unknown scalar width for type %q", sig.VarTypeName)
		}
		switch size {
		case 1:
			if sig.Ret == CInt {
				return IntValue(int64(*(*int8)(foreignPtr(sym)))), nil
			}
			return IntValue(int64(*(*uint8)(foreignPtr(sym)))), nil
		case 2:
			if sig.Ret == CInt {
				return IntValue(int64(*(*int16)(foreignPtr(sym)))), nil
			}
			return IntValue(int64(*(*uint16)(foreignPtr(sym)))), nil
		case 4:
			if sig.Ret == CInt {
				return IntValue(int64(*(*int32)(foreignPtr(sym)))), nil
			}
			return IntValue(int64(*(*uint32)(foreignPtr(sym)))), nil
		case 8:
			return IntValue(*(*int64)(foreignPtr(sym))), nil
		}
		return Null, fmt.Errorf("unsupported scalar width %d for type %q", size, sig.VarTypeName)
	}
	return Null, fmt.Errorf("unsupported extern kind for type %q", sig.VarTypeName)
}

// goCString copies the NUL-terminated C string at addr into a Go string.
func goCString(addr uintptr) string {
	var b []byte
	for i := uintptr(0); ; i++ {
		c := *(*byte)(foreignPtr(addr + i))
		if c == 0 {
			return string(b)
		}
		b = append(b, c)
	}
}

// ---- reflect-based C-type mapping ----

// point2D mirrors a C struct of two doubles (CGPoint/NSPoint/CGSize). purego
// builds the ABI-correct return path for small structs on amd64/arm64
// (registers), which is exactly where the Apple frameworks that return them
// run; elsewhere RegisterFunc panics and buildFFIFunc surfaces a bind error.
type point2D struct {
	X float64
	Y float64
}

// rect4D mirrors CGRect/NSRect: origin and size, four doubles. arm64 returns
// it as a homogeneous float aggregate in v0–v3; amd64 SysV uses a hidden sret
// pointer. purego builds both paths.
type rect4D struct {
	X float64
	Y float64
	W float64
	H float64
}

var (
	rtPoint2D = reflect.TypeOf(point2D{})
	rtRect4D  = reflect.TypeOf(rect4D{})
	rtBool    = reflect.TypeOf(false)
	rtInt64   = reflect.TypeOf(int64(0))
	rtUint64  = reflect.TypeOf(uint64(0))
	rtFloat32 = reflect.TypeOf(float32(0))
	rtFloat64 = reflect.TypeOf(float64(0))
	rtString  = reflect.TypeOf("")
	rtUintptr = reflect.TypeOf(uintptr(0))
)

func cTypeToReflect(k CType) reflect.Type {
	switch k {
	case CBool:
		return rtBool
	case CInt:
		return rtInt64
	case CUint:
		return rtUint64
	case CFloat:
		return rtFloat32
	case CDouble:
		return rtFloat64
	case CCharPtr:
		return rtString
	case CVoidPtr:
		return rtUintptr
	case CPoint2D:
		return rtPoint2D
	case CRect4D:
		return rtRect4D
	default:
		return rtUintptr
	}
}

func buzzToReflectArg(v Value, kind CType) (reflect.Value, error) {
	switch kind {
	case CBool:
		return reflect.ValueOf(v.Bool()), nil
	case CInt:
		switch v.tag() {
		case tagInt:
			return reflect.ValueOf(v.AsInt()), nil
		case tagFloat:
			// Upstream maps i64/u64 zdef params to Buzz `double`, so a single
			// source passes a double where a 64-bit C int is wanted. Accept it
			// here (truncating) so the same call marshals on both runtimes.
			return reflect.ValueOf(int64(v.AsFloat())), nil
		case tagBool:
			if v.AsBool() {
				return reflect.ValueOf(int64(1)), nil
			}
			return reflect.ValueOf(int64(0)), nil
		}
		return reflect.Value{}, fmt.Errorf("buzz: ffi: cannot convert %s to int", v.buzzKind())
	case CUint:
		switch v.tag() {
		case tagInt:
			return reflect.ValueOf(uint64(v.AsInt())), nil
		case tagFloat:
			return reflect.ValueOf(uint64(int64(v.AsFloat()))), nil
		case tagBool:
			if v.AsBool() {
				return reflect.ValueOf(uint64(1)), nil
			}
			return reflect.ValueOf(uint64(0)), nil
		}
		return reflect.Value{}, fmt.Errorf("buzz: ffi: cannot convert %s to uint", v.buzzKind())
	case CFloat:
		switch v.tag() {
		case tagFloat:
			return reflect.ValueOf(float32(v.AsFloat())), nil
		case tagInt:
			return reflect.ValueOf(float32(v.AsInt())), nil
		}
		return reflect.Value{}, fmt.Errorf("buzz: ffi: cannot convert %s to float", v.buzzKind())
	case CDouble:
		switch v.tag() {
		case tagFloat:
			return reflect.ValueOf(v.AsFloat()), nil
		case tagInt:
			return reflect.ValueOf(float64(v.AsInt())), nil
		}
		return reflect.Value{}, fmt.Errorf("buzz: ffi: cannot convert %s to double", v.buzzKind())
	case CCharPtr:
		if v.tag() == tagNull {
			return reflect.ValueOf(""), nil
		}
		if v.tag() != tagStr {
			return reflect.Value{}, fmt.Errorf("buzz: ffi: cannot convert %s to char*", v.buzzKind())
		}
		return reflect.ValueOf(v.asStr().V), nil
	case CVoidPtr:
		switch {
		case v.IsNull():
			return reflect.ValueOf(uintptr(0)), nil
		case v.IsUD():
			// Foreign pointer (`ud`): the full 64-bit address, losslessly boxed.
			return reflect.ValueOf(v.AsUD()), nil
		case v.IsInt():
			// A plain int address (e.g. a Buffer.ptr() into the Go heap, well
			// below 2^47) is still accepted.
			return reflect.ValueOf(uintptr(v.AsInt())), nil
		}
		return reflect.Value{}, fmt.Errorf("buzz: ffi: cannot convert %s to void*", v.buzzKind())
	}
	return reflect.Value{}, fmt.Errorf("buzz: ffi: unknown CType %d", kind)
}

func reflectRetToValue(r reflect.Value, kind CType) Value {
	switch kind {
	case CBool:
		// Bound as a uint64 word (see buildFFIFunc); C `bool`/`_Bool` is one
		// byte returned in the low bits, so test those.
		return BoolValue(r.Uint()&0xff != 0)
	case CInt:
		return IntValue(r.Int())
	case CUint:
		return IntValue(int64(r.Uint()))
	case CFloat, CDouble:
		return FloatValue(r.Float())
	case CCharPtr:
		return StrValue(r.String())
	case CVoidPtr:
		// A null pointer (address 0) surfaces as Buzz `null`, not 0, so
		// `handle != null` nil-checks behave like upstream buzz (a zdef
		// `?*anyopaque` return is a `ud?` compared against null). Non-null
		// addresses are carried as a heap-boxed `ud` (udObj.Addr, a uintptr) to
		// preserve the full 64-bit pointer; a NaN-boxed int truncates above 2^47.
		addr := r.Uint()
		if addr == 0 {
			return Null
		}
		return UDValue(uintptr(addr))
	case CPoint2D:
		// Field-named map (x, y) rather than a positional list: upstream buzz
		// returns a by-value struct accessed as `p.x`/`p.y`, so a map lets one
		// source read the same fields on both runtimes.
		p := r.Interface().(point2D)
		m := NewMap()
		m.MapSet("x", FloatValue(p.X))
		m.MapSet("y", FloatValue(p.Y))
		return m
	case CRect4D:
		// Field-named map (x, y, w, h); see CPoint2D above.
		rc := r.Interface().(rect4D)
		m := NewMap()
		m.MapSet("x", FloatValue(rc.X))
		m.MapSet("y", FloatValue(rc.Y))
		m.MapSet("w", FloatValue(rc.W))
		m.MapSet("h", FloatValue(rc.H))
		return m
	}
	return Null
}

// buildFFIFunc creates a Buzz DirectValue that calls a C function via
// purego.RegisterFunc. purego builds a proper ABI stub (XMM regs for
// float/double on amd64/arm64).
//
// purego.RegisterFunc panics on declarations it cannot bind (e.g. >1 return
// value, float returns on unsupported arches). We recover that bind-time panic
// and return it as an error so a malformed-but-parseable C decl fails the Buzz
// script rather than the host process. Runtime faults from the C call itself
// (segfaults) are not Go panics and remain inherently unrecoverable.
func buildFFIFunc(sig CFuncSig, sym uintptr) (fn Value, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("buzz: ffi: cannot bind %s(): %v", sig.Name, r)
		}
	}()
	paramTypes := make([]reflect.Type, len(sig.Params))
	for i, p := range sig.Params {
		paramTypes[i] = cTypeToReflect(p.Type)
	}
	var retTypes []reflect.Type
	if sig.Ret != CVoid {
		rt := cTypeToReflect(sig.Ret)
		if sig.Ret == CBool {
			// purego's reflect.Bool return path mis-marshals on arm64 (corrupts
			// register state, producing garbage out-params and crashing under
			// repeated calls). Receive the byte in a full word instead and test
			// its low bits in reflectRetToValue.
			rt = rtUint64
		}
		retTypes = []reflect.Type{rt}
	}
	fnType := reflect.FuncOf(paramTypes, retTypes, false)
	fnPtrVal := reflect.New(fnType)
	purego.RegisterFunc(fnPtrVal.Interface(), sym)
	cfn := fnPtrVal.Elem()

	return DirectValue(sig.Name, func(_ context.Context, args []Value) (Value, error) {
		if len(args) < len(sig.Params) {
			return Null, fmt.Errorf("buzz: ffi: %s() requires %d arguments, got %d",
				sig.Name, len(sig.Params), len(args))
		}
		in := make([]reflect.Value, len(sig.Params))
		for i, p := range sig.Params {
			rv, err := buzzToReflectArg(args[i], p.Type)
			if err != nil {
				return Null, fmt.Errorf("buzz: ffi: %s() arg %d: %w", sig.Name, i, err)
			}
			in[i] = rv
		}
		out := cfn.Call(in)
		if sig.Ret == CVoid {
			return Null, nil
		}
		return reflectRetToValue(out[0], sig.Ret), nil
	}), nil
}

// openLib resolves a zdef library NAME the way upstream buzz does: a bare name
// is run through the same search-path templates (`./?.!`, `/usr/lib/?.!`,
// `./lib?.!`, …, with `!` the platform's shared-library suffix), so the same
// `zdef("objc", …)` / `zdef("System", …)` strings resolve here and type-check
// on upstream. Two gopherbuzz-only additions keep the source single-sourced
// without diverging from upstream's spelling: a macOS framework fallback
// (`<name>.framework/<name>` — upstream can't name a framework, but `buzz -c`
// never opens the lib so it stays valid there) and the legacy versioned `.so.N`
// candidates. A name containing `/` is treated as a path and tried verbatim.
func openLib(name string) (uintptr, error) {
	ext := "so"
	if runtime.GOOS == "darwin" {
		ext = "dylib"
	}
	var candidates []string
	if strings.Contains(name, "/") {
		candidates = []string{name} // explicit path
	} else {
		for _, tmpl := range []string{
			"./%[1]s.%[2]s", "/usr/lib/%[1]s.%[2]s", "/usr/local/lib/%[1]s.%[2]s",
			"./lib%[1]s.%[2]s", "/usr/lib/lib%[1]s.%[2]s", "/usr/local/lib/lib%[1]s.%[2]s",
		} {
			candidates = append(candidates, fmt.Sprintf(tmpl, name, ext))
		}
		if runtime.GOOS == "darwin" {
			candidates = append(candidates,
				"/System/Library/Frameworks/"+name+".framework/"+name)
		}
		// Legacy/robustness: the bare name (dyld may resolve a leaf), versioned
		// Linux sonames, and the opposite lib-prefix form.
		candidates = append(candidates, name,
			"lib"+name+".so.6", "lib"+name+".so.1", "lib"+name+".so.0",
			name+".so.6", name+".so.1", name+".so.0")
	}
	var lastErr error
	for _, path := range candidates {
		h, err := purego.Dlopen(path, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err == nil {
			return h, nil
		}
		lastErr = err
	}
	return 0, fmt.Errorf("buzz: ffi: cannot open library %q: %w", name, lastErr)
}
