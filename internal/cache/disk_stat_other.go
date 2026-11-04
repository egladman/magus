//go:build !unix && !windows

package cache

import (
	"errors"
	"runtime"
)

// StatDisk fails on a platform with no disk statistics (js/wasm), so no caller
// can mistake the zero values for an empty or a full disk.
func StatDisk(path string) (total, free int64, err error) {
	return 0, 0, errors.New("disk statistics are unavailable on " + runtime.GOOS + "/" + runtime.GOARCH + ": cannot read " + path)
}
