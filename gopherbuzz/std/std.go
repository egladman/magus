// Package std provides Buzz's standard library modules as synthetic modules
// for the magus/buzz interpreter.
//
// Call [Register] once after creating a session to make all modules available
// for import:
//
//	import "std"       // assert, print, parseInt, toInt, char, random, panic, …
//	import "math"      // sin, cos, sqrt, pi, abs, …
//	import "fs"        // currentDirectory, makeDirectory, delete, move, list, exists
//	import "os"        // sleep, time, env, tmpDir, tmpFilename, exit, execute, Socket, TcpServer
//	import "crypto"    // HashAlgorithm enum + hash()
//	import "gc"        // allocated, collect
//	import "debug"     // dump, ast
//	import "io"        // File, FileMode, stdin, stdout, stderr, runFile
//	import "serialize" // Boxed, serialize, jsonEncode, jsonDecode
//	import "buffer"    // Buffer
//
// ffi is registered with stub implementations that return a descriptive error:
// sizeOf, alignOf, sizeOfStruct, and alignOfStruct require the Zig ABI and
// cannot be implemented in a Go embedding.
package std

import (
	"context"
	"fmt"

	buzz "github.com/egladman/gopherbuzz"
)

func Register(sess *buzz.Session) {
	sess.SetSyntheticModule("std", coreModule())
	sess.SetSyntheticModule("math", mathModule())
	sess.SetSyntheticModule("fs", fsModule())
	sess.SetSyntheticModule("os", osModule())
	sess.SetSyntheticModule("crypto", cryptoModule())
	sess.SetSyntheticModule("gc", gcModule())
	sess.SetSyntheticModule("debug", debugModule())
	sess.SetSyntheticModule("io", ioModule(sess))
	sess.SetSyntheticModule("serialize", serializeModule())
	sess.SetSyntheticModule("buffer", bufferModule())
	sess.SetSyntheticModule("ffi", ffiStubModule())
}

func fn(name string, f func(context.Context, []buzz.Value) (buzz.Value, error)) buzz.Value {
	return buzz.DirectValue(name, f)
}

func mod() buzz.Value { return buzz.NewMap() }

// ffiStubModule returns the "ffi" module with each known member wired to a stub
// that returns a clear error. sizeOf/alignOf/sizeOfStruct/alignOfStruct all
// require the Zig ABI and cannot be implemented in a Go embedding.
func ffiStubModule() buzz.Value {
	const reason = "FFI requires the Zig ABI, not supported in the magus/buzz embedding"
	stub := fn("ffi.stub", func(_ context.Context, _ []buzz.Value) (buzz.Value, error) {
		return buzz.Null, fmt.Errorf("ffi: %s", reason)
	})
	m := mod()
	for _, name := range []string{"sizeOf", "alignOf", "sizeOfStruct", "alignOfStruct"} {
		m.MapSet(name, stub)
	}
	return m
}
