//go:build darwin

package run

import (
	"os"
	"syscall"
)

// maxRSSBytes returns the peak resident set size of a finished process, in BYTES.
//
// Darwin reports ru_maxrss in bytes already. Linux and the BSDs report kilobytes, which
// is the whole reason this lives in a per-platform file rather than behind a runtime
// GOOS check: the two differ by 1000x, silently, in a field with the same name. Doing the
// conversion at the platform boundary means every caller above sees one unit and cannot
// get it wrong.
func maxRSSBytes(ps *os.ProcessState) int64 {
	if ps == nil {
		return 0
	}
	ru, ok := ps.SysUsage().(*syscall.Rusage)
	if !ok || ru == nil {
		return 0
	}
	// No conversion: darwin's Maxrss is already int64. The unix build keeps one because
	// the field is int32 on 32-bit arches there.
	return ru.Maxrss
}
