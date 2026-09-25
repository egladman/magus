package sandbox

import "io/fs"

// ownedByUser reports true: Windows has no uid to compare, and its per-user temp dir
// is not shared between accounts.
func ownedByUser(fs.FileInfo) bool { return true }
