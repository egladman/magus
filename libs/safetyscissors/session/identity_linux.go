package session

import (
	"os"
	"syscall"
)

// inode is fi's inode number, or zero where the platform reports none.
func inode(fi os.FileInfo) uint64 {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return st.Ino
}
