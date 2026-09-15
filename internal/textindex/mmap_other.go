//go:build !unix

package textindex

import (
	"fmt"
	"os"
)

// mmapSupported reports whether this build can map a file instead of copying it.
const mmapSupported = false

// mapFile always fails here; Reader falls back to a copying read, which is correct
// everywhere and merely slower.
func mapFile(*os.File, int) ([]byte, error) {
	return nil, fmt.Errorf("textindex: mmap is not available on this platform")
}

// unmapFile has nothing to release, because mapFile never succeeds.
func unmapFile([]byte) error { return nil }
