//go:build !wasm

package file

import "os"

// Chmod sets a file's mode. On every real build target this is just os.Chmod; it is a
// package function (rather than a direct os.Chmod at each call site) only so the wasm
// build - TinyGo's js/wasm os package has no Chmod - can substitute a no-op (see
// chmod_wasm.go). Packages that compile into the Buzz playground wasm (internal/file
// itself, internal/cache) call this instead of os.Chmod so the undefined symbol
// disappears from that build, not merely goes uncalled.
func Chmod(name string, perm os.FileMode) error { return os.Chmod(name, perm) }
