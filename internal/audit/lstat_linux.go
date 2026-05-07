package audit

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// atFDCWD is unix.AT_FDCWD widened through a non-const int variable so
// the negative constant can be reinterpreted as a uintptr (the kernel
// treats it as a special-cased sentinel for "current working directory").
var atFDCWD = func() uintptr {
	v := int64(unix.AT_FDCWD)
	return uintptr(v)
}()

// lstatMtimeSize fills mtime (nanoseconds since epoch) and size for pathBuf via SYS_NEWFSTATAT;
// does not follow symlinks. Requires cap(pathBuf) > len(pathBuf) (invariant held by walkDir)
// to null-terminate in-place without reallocation. Returns ok=false on any error.
func lstatMtimeSize(pathBuf []byte) (modTimeNs int64, size int64, ok bool) {
	// Write a null terminator one past the logical end.
	pathBuf = pathBuf[:len(pathBuf)+1]
	pathBuf[len(pathBuf)-1] = 0
	var st unix.Stat_t
	_, _, errno := unix.Syscall6(
		unix.SYS_NEWFSTATAT,
		atFDCWD,
		uintptr(unsafe.Pointer(&pathBuf[0])),
		uintptr(unsafe.Pointer(&st)),
		uintptr(unix.AT_SYMLINK_NOFOLLOW),
		0, 0,
	)
	if errno != 0 {
		return 0, 0, false
	}
	return st.Mtim.Sec*1_000_000_000 + st.Mtim.Nsec, st.Size, true
}
