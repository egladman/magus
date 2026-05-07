//go:build !linux

package cache

// hashFilesIoUring is a no-op stub on non-Linux platforms. Returns false
// so hashFiles always uses the generic goroutine pool on this OS.
func (c *Cache) hashFilesIoUring(_ []relAbs, _ []string) bool { return false }
