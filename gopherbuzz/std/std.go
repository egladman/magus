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

// Modules is the single source of truth for the modules gopherbuzz bundles: the
// upstream-faithful stdlib (buzz.LabelUpstream) plus gopherbuzz's own test surface
// (buzz.LabelGopherbuzz). Register provides every entry; a caller filters by label
// for a subset. Edit this table to add a module. The registration shape is
// buzz.Module (see gopherbuzz/module.go), shared with host embedders.
var Modules = []buzz.Module{
	{Name: "std", Labels: []string{buzz.LabelUpstream}, Bind: func(s *buzz.Session, env buzz.ModuleEnv) error {
		s.SetSyntheticModule("std", coreModule(env.Out)) // std.print targets env.Out
		return nil
	}},
	{Name: "math", Labels: []string{buzz.LabelUpstream}, Bind: synthetic("math", mathModule)},
	{Name: "fs", Labels: []string{buzz.LabelUpstream}, Bind: synthetic("fs", fsModule)},
	{Name: "os", Labels: []string{buzz.LabelUpstream}, Bind: synthetic("os", osModule)},
	{Name: "crypto", Labels: []string{buzz.LabelUpstream}, Bind: synthetic("crypto", cryptoModule)},
	{Name: "gc", Labels: []string{buzz.LabelUpstream}, Bind: synthetic("gc", gcModule)},
	{Name: "debug", Labels: []string{buzz.LabelUpstream}, Bind: synthetic("debug", debugModule)},
	{Name: "io", Labels: []string{buzz.LabelUpstream}, Bind: func(s *buzz.Session, _ buzz.ModuleEnv) error {
		s.SetSyntheticModule("io", ioModule(s)) // io binds against the session
		return nil
	}},
	{Name: "serialize", Labels: []string{buzz.LabelUpstream}, Bind: synthetic("serialize", serializeModule)},
	{Name: "buffer", Labels: []string{buzz.LabelUpstream}, Bind: synthetic("buffer", bufferModule)},
	{Name: "ffi", Labels: []string{buzz.LabelUpstream}, Bind: synthetic("ffi", ffiModule)},
	{Name: "assertcore", Labels: []string{buzz.LabelGopherbuzz}, Bind: synthetic("assertcore", assertCoreModule)},
	{Name: "assert", Labels: []string{buzz.LabelGopherbuzz}, Bind: source("assert", assertSource)},
	{Name: "suite", Labels: []string{buzz.LabelGopherbuzz}, Bind: source("suite", suiteSource)},
	{Name: "testing", Labels: []string{buzz.LabelGopherbuzz}, Bind: source("testing", testingSource)},
}

// synthetic returns a Bind that installs a synthetic (host-value) module built by
// make -- the common case, where the module needs nothing from the ModuleEnv.
func synthetic(name string, make func() vm.Value) func(*buzz.Session, buzz.ModuleEnv) error {
	return func(s *buzz.Session, _ buzz.ModuleEnv) error {
		s.SetSyntheticModule(name, make())
		return nil
	}
}

// source returns a Bind that installs an embedded .buzz source module.
func source(name, src string) func(*buzz.Session, buzz.ModuleEnv) error {
	return func(s *buzz.Session, _ buzz.ModuleEnv) error {
		s.SetSourceModule(name, src)
		return nil
	}
}

// Register installs every bundled module on sess. std.print writes to os.Stdout;
// use RegisterWithOutput to redirect it.
func Register(sess *buzz.Session) { RegisterWithOutput(sess, os.Stdout) }

// RegisterWithOutput is Register with std.print directed to out. An embedding
// that captures a program's textual output (e.g. the WebAssembly playground)
// passes its own writer so print lands in a buffer instead of the host stdout.
func RegisterWithOutput(sess *buzz.Session, out io.Writer) {
	// std modules never fail to bind; the (always-nil) error is dropped to keep
	// this a void call. A host that provides fallible modules uses sess.Provide.
	_ = sess.Provide(buzz.ModuleEnv{Ctx: context.Background(), Out: out}, Modules...)
}

func fn(name string, f func(context.Context, []vm.Value) (vm.Value, error)) vm.Value {
	return vm.DirectValue(name, f)
}

func mod() vm.Value { return vm.NewMap() }
