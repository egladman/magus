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
	"strings"

	"github.com/ebitengine/purego"
)

// init registers the purego backend as the default FFI provider on supported
// platforms. An embedder can still override it via RegisterFFIProvider.
func init() { RegisterFFIProvider(puregoFFI{}) }

// puregoFFI implements FFIProvider using purego's dlopen/dlsym + RegisterFunc.
type puregoFFI struct{}

// OpenLibrary opens libname and binds each signature, returning a Buzz map of
// function name -> direct callable.
func (puregoFFI) OpenLibrary(libname string, sigs []CFuncSig) (Value, error) {
	handle, err := openLib(libname)
	if err != nil {
		return Null, err
	}
	m := newMapObj()
	for _, sig := range sigs {
		sym, err := purego.Dlsym(handle, sig.Name)
		if err != nil {
			return Null, fmt.Errorf("buzz: ffi: symbol %q not found in %q: %w", sig.Name, libname, err)
		}
		fn, err := buildFFIFunc(sig, sym)
		if err != nil {
			return Null, err
		}
		m.set(sig.Name, fn)
	}
	return heapValue(tagMap, m), nil
}

// ---- reflect-based C-type mapping ----

var (
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
		if v.tag() == tagNull {
			return reflect.ValueOf(uintptr(0)), nil
		}
		if v.tag() == tagInt {
			return reflect.ValueOf(uintptr(v.AsInt())), nil
		}
		return reflect.Value{}, fmt.Errorf("buzz: ffi: cannot convert %s to void*", v.buzzKind())
	}
	return reflect.Value{}, fmt.Errorf("buzz: ffi: unknown CType %d", kind)
}

func reflectRetToValue(r reflect.Value, kind CType) Value {
	switch kind {
	case CBool:
		return BoolValue(r.Bool())
	case CInt:
		return IntValue(r.Int())
	case CUint:
		return IntValue(int64(r.Uint()))
	case CFloat, CDouble:
		return FloatValue(r.Float())
	case CCharPtr:
		return StrValue(r.String())
	case CVoidPtr:
		return IntValue(int64(r.Uint()))
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
		retTypes = []reflect.Type{cTypeToReflect(sig.Ret)}
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

// openLib opens a shared library by name, trying common suffixes/prefixes.
func openLib(name string) (uintptr, error) {
	candidates := []string{name}
	if !strings.Contains(name, "/") && !strings.Contains(name, ".") {
		if strings.HasPrefix(name, "lib") {
			candidates = append(candidates,
				name+".so",
				name+".so.6",
				name+".so.1",
				name+".so.0",
				name+".dylib",
			)
		} else {
			candidates = append(candidates,
				"lib"+name+".so",
				"lib"+name+".so.6",
				"lib"+name+".so.1",
				"lib"+name+".so.0",
				"lib"+name+".dylib",
			)
		}
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
