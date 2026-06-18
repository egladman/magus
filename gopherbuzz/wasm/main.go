//go:build wasm

// Command buzz-wasm is a minimal WebAssembly entry point for the interpreter.
// It reads a Buzz program from stdin, evaluates it in a fresh session, and
// writes the value of a trailing `return <expr>` to stdout (Null prints
// nothing-but-a-newline). Any parse, type, or runtime error goes to stderr and
// exits non-zero. It exists to demonstrate — and keep CI honest about — the
// fact that the interpreter compiles and runs unmodified inside a WASI runtime.
//
// The `wasm` build constraint is the GOARCH=wasm tag, set by both TinyGo
// (-target=wasi/-target=wasm) and the standard toolchain (GOARCH=wasm). It
// keeps this main package out of the host `go build ./...` so the module stays
// a pure library on native platforms.
//
// Build with TinyGo (recommended — ~1.6 MB binary). The default scheduler is
// required: the interpreter spawns goroutines for fibers, so -scheduler=none
// will not link.
//
//	tinygo build -target=wasi -o buzz.wasm ./wasm
//
// Build with the standard toolchain (~4 MB binary, no extra toolchain):
//
//	GOOS=wasip1 GOARCH=wasm go build -o buzz.wasm ./wasm
//
// Run under any WASI runtime:
//
//	echo 'return 1 + 2;' | wasmtime buzz.wasm   # prints 3
package main

import (
	"context"
	"fmt"
	"io"
	"os"

	buzz "github.com/egladman/gopherbuzz"
)

func main() {
	src, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, "buzz-wasm: read stdin:", err)
		os.Exit(1)
	}

	ctx := context.Background()
	s := buzz.NewSession(ctx, buzz.WithEmbedded())
	v, err := s.Eval(ctx, string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, "buzz-wasm:", err)
		os.Exit(1)
	}
	fmt.Println(v.String())
}
