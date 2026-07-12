//go:build wasm

package file

import "os"

// Chmod is a no-op on wasm: TinyGo's js/wasm os package has no Chmod, and the playground's
// sandboxed in-memory filesystem has no permission bits to honor. See chmod.go for the real
// (os.Chmod) implementation.
func Chmod(_ string, _ os.FileMode) error { return nil }
