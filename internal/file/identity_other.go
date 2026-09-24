//go:build !unix

package file

import "os"

// Identity returns fi's inode and hard-link count. ok is false where the platform
// reports neither.
func Identity(os.FileInfo) (ino, nlink uint64, ok bool) { return 0, 0, false }
