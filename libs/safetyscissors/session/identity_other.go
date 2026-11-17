//go:build !linux && !darwin

package session

import "os"

// inode is zero here: the platform reports no inode, so a cached fold keys on size and
// modification time alone.
func inode(os.FileInfo) uint64 { return 0 }
