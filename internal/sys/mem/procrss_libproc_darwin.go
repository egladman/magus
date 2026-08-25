//go:build darwin && !noffi

package mem

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// procPidinfo is libSystem's own proc_pidinfo, resolved once at first use.
//
// The supported route: Apple promises the libSystem wrapper and explicitly not the
// trap beneath it, which is what staticcheck's SA1019 says about calling
// SYS_PROC_INFO directly. purego reaches libSystem without cgo, and is already how
// Buzz FFI works here, so this costs no new dependency and no C toolchain.
//
// It does cost a PT_INTERP, which is why this file is `!noffi` and
// procrss_syscall_darwin.go exists: the archives magus ships as static must have
// no dynamic loader, and that build takes the syscall instead.
var procPidinfo = sync.OnceValue(func() uintptr {
	fn, err := purego.Dlsym(purego.RTLD_DEFAULT, "proc_pidinfo")
	if err != nil {
		return 0
	}
	return fn
})

// procRSS reports one process's resident bytes, or 0 when it cannot be read. A
// process that exited between the table read and this call is the ordinary case,
// not an error.
func procRSS(pid int32) int64 {
	fn := procPidinfo()
	if fn == 0 {
		return 0
	}
	var ti procTaskInfo
	size := unsafe.Sizeof(ti)
	n, _, _ := purego.SyscallN(fn,
		uintptr(pid), procPidTaskInfo, 0, uintptr(unsafe.Pointer(&ti)), size)
	if n < size {
		return 0
	}
	return int64(ti.ResidentSize)
}
