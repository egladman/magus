//go:build !windows

package confinement

import (
	"io/fs"
	"os"
	"syscall"
)

func ownedByUser(info fs.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
