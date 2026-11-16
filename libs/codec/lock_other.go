//go:build (!unix && !windows) || aix || hurd

package codec

import (
	"errors"
	"os"
	"runtime"
)

// tryLock refuses: this platform has no OS file lock the module knows, and a store
// written without one could interleave two writers' rewrites.
func tryLock(*os.File) error {
	return errors.New("no OS file lock on " + runtime.GOOS)
}

func unlock(*os.File) error { return nil }
