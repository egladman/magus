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
// ffi is C-ABI native (see ffi.go): zdef() binds C functions, and the ffi module
// offers cstr, sizeOf/alignOf, sizeOfStruct/alignOfStruct, structLayout, and a
// pinned alloc/free/read/write memory API for out-parameters and by-reference
// structs. Type arguments are C type-name strings, not Zig types.
package std

import (
	"context"
	"io"
	"os"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
)

// Register installs every std module on sess. std.print writes to os.Stdout;
// use RegisterWithOutput to redirect it.
func Register(sess *buzz.Session) { RegisterWithOutput(sess, os.Stdout) }

// RegisterWithOutput is Register with std.print directed to out. An embedding
// that captures a program's textual output (e.g. the WebAssembly playground)
// passes its own writer so print lands in a buffer instead of the host stdout.
func RegisterWithOutput(sess *buzz.Session, out io.Writer) {
	sess.SetSyntheticModule("std", coreModule(out))
	sess.SetSyntheticModule("math", mathModule())
	sess.SetSyntheticModule("fs", fsModule())
	sess.SetSyntheticModule("os", osModule())
	sess.SetSyntheticModule("crypto", cryptoModule())
	sess.SetSyntheticModule("gc", gcModule())
	sess.SetSyntheticModule("debug", debugModule())
	sess.SetSyntheticModule("io", ioModule(sess))
	sess.SetSyntheticModule("serialize", serializeModule())
	sess.SetSyntheticModule("buffer", bufferModule())
	sess.SetSyntheticModule("ffi", ffiModule())
}

func fn(name string, f func(context.Context, []vm.Value) (vm.Value, error)) vm.Value {
	return vm.DirectValue(name, f)
}

func mod() vm.Value { return vm.NewMap() }
