//go:build !((darwin || freebsd || netbsd || (linux && (386 || amd64 || arm || arm64 || loong64 || ppc64le || riscv64 || (cgo && s390x)))) && !android && !noffi)

package vm

import (
	"context"
	"fmt"
)

// This is the platform half of ffi.go for targets purego cannot dlopen on: zdef()
// reports FFI unsupported until an embedder calls RegisterFFIProvider. See
// ffi_purego.go for the tag this negates.

func defaultFFIProvider() FFIProvider { return nil }

func makeCallback(context.Context, Value, CType, []CType) (uintptr, error) {
	return 0, fmt.Errorf("buzz: ffi: callbacks are not supported on this platform (no FFI provider registered)")
}
