//go:build cgo_engines

// Umka bindings for the comparison harness, via cgo. The Umka C99 source is
// vendored under internal/umka and compiled as one translation unit through the
// amalgamation (see internal/umka/umka_amalg.c), so no loose .c files sit in the
// Go package directory. Compiled only under -tags cgo_engines.
//
// Umka is a statically typed interpreter (not a JIT); it executes a compiled
// bytecode program. umkaRun is re-runnable, so the warm path compiles once and
// re-runs the same instance.
package comparison

/*
#cgo CFLAGS: -I${SRCDIR}/internal/umka -O2 -w
#cgo LDFLAGS: -lm
#include <stdlib.h>
#include "umka_amalg.c"
#include "umka_api.h"

// uSetup allocs, inits, and compiles src; on failure *errOut points at the Umka
// error message (owned by the instance). implLibsEnabled=1 gives builtin sqrt,
// sprintf, etc.; fileSystemEnabled=0 sandboxes it.
static Umka *uSetup(const char *name, const char *src, const char **errOut) {
    Umka *u = umkaAlloc();
    if (!umkaInit(u, name, src, 2*1024*1024, NULL, 0, NULL, 0, 1, NULL)) { *errOut = umkaGetError(u)->msg; return u; }
    if (!umkaCompile(u))                                                 { *errOut = umkaGetError(u)->msg; return u; }
    *errOut = NULL;
    return u;
}
static const char *uRun(Umka *u) { return umkaRun(u) == 0 ? NULL : umkaGetError(u)->msg; }
*/
import "C"

import "unsafe"

// umkaState owns one Umka instance and the C strings it was built from (kept
// alive for the instance's lifetime). The C pointer never crosses into a
// _test.go file; only these methods do.
type umkaState struct {
	u          *C.Umka
	name, csrc *C.char
}

// umkaNew compiles src into a fresh instance. A non-empty string is an error.
func umkaNew(src string) (umkaState, string) {
	name := C.CString("bench.um")
	csrc := C.CString(src)
	var cerr *C.char
	u := C.uSetup(name, csrc, &cerr)
	if cerr != nil {
		errStr := C.GoString(cerr)
		C.umkaFree(u)
		C.free(unsafe.Pointer(name))
		C.free(unsafe.Pointer(csrc))
		return umkaState{}, errStr
	}
	return umkaState{u: u, name: name, csrc: csrc}, ""
}

// run executes main once. A non-empty string is a runtime error.
func (s umkaState) run() string {
	if e := C.uRun(s.u); e != nil {
		return C.GoString(e)
	}
	return ""
}

func (s umkaState) free() {
	C.umkaFree(s.u)
	C.free(unsafe.Pointer(s.name))
	C.free(unsafe.Pointer(s.csrc))
}
