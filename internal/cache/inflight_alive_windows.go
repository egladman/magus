//go:build windows

package cache

import "os"

// processAlive on Windows: FindProcess only succeeds for a live process, unlike unix
// where it always succeeds and the signal does the work.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}
