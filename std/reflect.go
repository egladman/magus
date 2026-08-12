package std

import (
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
)

// MethodSource returns the source file (relative to repoRoot) and line where a
// method's Impl is defined, for linking generated docs back to the code. Returns
// ("", 0) when Impl is nil/not a function, or its file resolves outside repoRoot
// (e.g. a -trimpath build, where there's no absolute path to relativize).
func MethodSource(m Method, repoRoot string) (string, int) {
	if m.Impl == nil {
		return "", 0
	}
	rv := reflect.ValueOf(m.Impl)
	if rv.Kind() != reflect.Func {
		return "", 0
	}
	fn := runtime.FuncForPC(rv.Pointer())
	if fn == nil {
		return "", 0
	}
	file, line := fn.FileLine(rv.Pointer())
	rel, err := filepath.Rel(repoRoot, file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", 0
	}
	return filepath.ToSlash(rel), line
}

// implName returns the package-qualified Go name of a method's Impl, e.g.
// "std.FsGlob" — the import path is stripped. Empty when Impl is nil or not a
// function.
func implName(m Method) string {
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
func MethodFuncName(m Method) string {
	return bareName(implName(m))
}

// FieldFuncName returns the bare function name of f.Resolver (e.g. "VcsName").
func FieldFuncName(f Field) string {
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
func FieldResolverTakesCtx(f Field) bool {
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

// MethodImplPackage returns the Go import path and local package identifier
// of m.Impl - e.g. ("github.com/egladman/magus/std", "std") for a method still
// implemented in std's own flat root, or
// ("github.com/egladman/magus/std/encoding/json", "json") for one implemented
// in a std/encoding leaf package. Both empty if Impl is nil or not a function.
//
// The magus-utils bindings generator used to assume every Impl lived in
// package std and hardcoded the "std." call qualifier; that stopped holding
// once std/encoding split nine modules into their own packages, so the
// qualifier - and the import it needs - now has to come from wherever the
// Impl actually is.
func MethodImplPackage(m Method) (importPath, pkgIdent string) {
	return implPackage(m.Impl)
}

// FieldResolverPackage is MethodImplPackage for a Field's Resolver.
func FieldResolverPackage(f Field) (importPath, pkgIdent string) {
	return implPackage(f.Resolver)
}

// implPackage derives (importPath, pkgIdent) from a Method.Impl or
// Field.Resolver function value's runtime-reported name, which has the shape
// "full/import/path.FuncName" (or "...FuncName-fm" for a method value). The
// function name itself never contains a dot or a slash, so splitting on the
// LAST dot separates the import path from the func name unambiguously, and
// the segment after the import path's last slash is the package identifier a
// caller in another file would write.
func implPackage(fn any) (importPath, pkgIdent string) {
	if fn == nil {
		return "", ""
	}
	rv := reflect.ValueOf(fn)
	if rv.Kind() != reflect.Func {
		return "", ""
	}
	full := runtime.FuncForPC(rv.Pointer()).Name()
	dot := strings.LastIndexByte(full, '.')
	if dot < 0 {
		return "", ""
	}
	importPath = full[:dot]
	pkgIdent = importPath
	if slash := strings.LastIndexByte(importPath, '/'); slash >= 0 {
		pkgIdent = importPath[slash+1:]
	}
	return importPath, pkgIdent
}
