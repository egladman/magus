//go:build !unix

package audit

import "os"

// inodeOf reports no identity where the platform offers none, so the audit compares
// mtime and size alone there.
func inodeOf(os.FileInfo) uint64 { return 0 }
