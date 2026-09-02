//go:build windows

package pid

import "os"

// Alive on Windows: FindProcess only succeeds for a live process, unlike unix
// where it always succeeds and the signal does the work.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}
