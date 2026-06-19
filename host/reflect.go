package host

import (
	"reflect"
	"runtime"

	"github.com/egladman/magus/std"
)

// implName returns the package-qualified Go name of a method's Impl, e.g.
// "std.FsGlob" — the import path is stripped. Empty when Impl is nil or not a
// function.
func implName(m std.Method) string {
	if m.Impl == nil {
		return ""
	}
	rv := reflect.ValueOf(m.Impl)
	if rv.Kind() != reflect.Func {
		return ""
	}
	full := runtime.FuncForPC(rv.Pointer()).Name()
	// full is like "github.com/.../std.FsGlob". Drop everything up to and
	// including the last "/", then keep "std.FsGlob" (or "std.FsGlob-fm" for
	// method values).
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == '/' {
			return full[i+1:]
		}
	}
	return full
}

// MethodFuncName returns just the bare function name of m's Impl (e.g. "FsGlob")
// without the package qualifier. Useful for generating trampoline names.
func MethodFuncName(m std.Method) string {
	return bareName(implName(m))
}

// FieldFuncName returns the bare function name of f.Resolver (e.g. "VcsName").
func FieldFuncName(f std.Field) string {
	if f.Resolver == nil {
		return ""
	}
	full := runtime.FuncForPC(reflect.ValueOf(f.Resolver).Pointer()).Name()
	for i := len(full) - 1; i >= 0; i-- {
		if full[i] == '/' {
			full = full[i+1:]
			break
		}
	}
	return bareName(full)
}

// FieldResolverTakesCtx reports whether f.Resolver's signature is
// (context.Context) (T, error) rather than () (T, error).
func FieldResolverTakesCtx(f std.Field) bool {
	if f.Resolver == nil {
		return false
	}
	return reflect.TypeOf(f.Resolver).NumIn() == 1
}

// bareName drops a "pkg." qualifier, returning the segment after the first dot.
func bareName(qualified string) string {
	for i := 0; i < len(qualified); i++ {
		if qualified[i] == '.' {
			return qualified[i+1:]
		}
	}
	return qualified
}
