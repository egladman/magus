//go:build !linux

package audit

import (
	"os"
)

// lstatMtimeSize is the portable fallback for the linux fast path. See
// lstat_linux.go for the contract and rationale. This path does not
// achieve the alloc reduction of the linux variant; the win is gated on
// syscall.Stat_t's per-OS shape.
func lstatMtimeSize(pathBuf []byte) (modTimeNs int64, size int64, ok bool) {
	info, err := os.Lstat(string(pathBuf))
	if err != nil {
		return 0, 0, false
	}
	return info.ModTime().UnixNano(), info.Size(), true
}
