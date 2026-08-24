//go:build darwin && noffi

package mem

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// procInfoCallPidInfo is PROC_INFO_CALL_PIDINFO, the selector libSystem's
// proc_pidinfo passes to the proc_info syscall.
const procInfoCallPidInfo = 2

// procRSS reports one process's resident bytes, or 0 when it cannot be read.
//
// The raw trap, because this build has deliberately given up the ability to call
// libSystem: `noffi` strips purego so the binary has no PT_INTERP and the archive
// magus ships as static is honestly static. dlopen is not available to it by
// construction, so the choice here is the syscall or no figure at all.
//
// That is why SA1019 is suppressed rather than heeded. The dynamic build DOES heed
// it (see procrss_libproc_darwin.go); this file is the fallback for a build that
// cannot. proc_info is the call libSystem itself makes, and its number has been
// stable since 10.5, but Apple promises the wrapper rather than the trap, so a
// darwin release that breaks it degrades this to 0 (UNKNOWN) rather than crashing:
// every caller already treats 0 as unmeasured.
func procRSS(pid int32) int64 {
	var ti procTaskInfo
	size := unsafe.Sizeof(ti)
	n, _, errno := unix.Syscall6(unix.SYS_PROC_INFO, //nolint:staticcheck // SA1019: noffi builds cannot reach libSystem; see the doc comment
		procInfoCallPidInfo, uintptr(pid), procPidTaskInfo, 0,
		uintptr(unsafe.Pointer(&ti)), size)
	if errno != 0 || n < size {
		return 0
	}
	return int64(ti.ResidentSize)
}
