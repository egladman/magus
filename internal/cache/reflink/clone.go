// Package reflink copies regular files using the most efficient mechanism the
// host platform and filesystem provide, transparently falling back to a plain
// userspace copy where no acceleration is available.
//
// The platform-specific implementations live in the clone_<goos>.go files; this
// file owns the package's exported surface so callers see one documented API
// regardless of build target.
package reflink

// Clone copies the regular file src to dst. dst must not already exist; the
// caller is responsible for removing it first.
//
// Where the filesystem supports it, Clone performs an O(1) copy-on-write reflink
// (FICLONE on Linux, clonefile on APFS); otherwise it falls back to an in-kernel
// zero-copy splice or a userspace byte copy. On success dst holds the same
// contents as src.
func Clone(src, dst string) error { return clone(src, dst) }

// Probe reports whether the filesystem containing dir supports copy-on-write
// reflinks. It returns false when the probe cannot run (dir missing, temp files
// uncreatable) or the platform/filesystem has no reflink support.
func Probe(dir string) bool { return probe(dir) }
