package job

import (
	"os"
	"syscall"
)

// openNonBlocking opens path to read without waiting for a writer on a fifo.
func openNonBlocking(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
