//go:build !windows && !wasm && !darwin

package run

import (
	"os"
	"syscall"
)

// maxRSSBytes returns the peak resident set size of a finished process, in BYTES.
//
// Linux and the BSDs report ru_maxrss in KILOBYTES; darwin reports bytes. See the darwin
// build of this file: the conversion happens here, at the platform boundary, so no caller
// above ever has to know which host it is on.
func maxRSSBytes(ps *os.ProcessState) int64 {
	if ps == nil {
		return 0
	}
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0
	}
	return int64(ru.Maxrss) * 1024
}
