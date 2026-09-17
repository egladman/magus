//go:build unix

package textindex

import (
	"os"

	"golang.org/x/sys/unix"
)

// mmapSupported reports whether this build can map a file instead of copying it.
const mmapSupported = true

// mapFile maps size bytes of f read-only.
//
// MAP_SHARED rather than MAP_PRIVATE: nothing here writes, and a private mapping asks
// the kernel to prepare copy-on-write pages a read-only scan will never use.
func mapFile(f *os.File, size int) ([]byte, error) {
	return unix.Mmap(int(f.Fd()), 0, size, unix.PROT_READ, unix.MAP_SHARED)
}

// unmapFile releases a mapping returned by mapFile.
func unmapFile(b []byte) error { return unix.Munmap(b) }
