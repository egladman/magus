//go:build unix && (!linux || 386 || arm || mips || mipsle)

package audit

import (
	"os"
	"syscall"
)

func inodeOf(info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Ino) //nolint:unconvert // Ino is uint32 on some BSDs
	}
	return 0
}
