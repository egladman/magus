//go:build !linux || 386 || arm || mips || mipsle

package audit

import (
	"os"
)

// lstatFile is the portable fallback for the linux fast path. See
// lstat_linux.go for the contract and rationale. This path does not
// achieve the alloc reduction of the linux variant; the win is gated on
// syscall.Stat_t's per-OS shape, and on 32-bit Linux additionally on a
// syscall number and Mtim width that differ from the 64-bit arches.
func lstatFile(pathBuf []byte) (fileState, bool) {
	info, err := os.Lstat(string(pathBuf))
	if err != nil {
		return fileState{}, false
	}
	return fileState{modTimeNs: info.ModTime().UnixNano(), size: info.Size(), ino: inodeOf(info)}, true
}
