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

// Kind classifies a bundled module by provenance. It replaces the old
// Register/RegisterExtensions split: rather than two entry points, there is one
// table of modules each tagged with a Kind, and one Register that installs them
// all. A surface that wants only a subset (e.g. a strict-conformance run that
// wants the upstream-faithful surface) filters Modules by Kind itself.
type Kind int

const (
	// Upstream is a clean-room reimplementation of a module in upstream Buzz's
	// standard library; its names, signatures, and semantics track upstream.
	Upstream Kind = iota
	// Extension is a gopherbuzz-original module with no upstream counterpart
	// (the value-aware test surface: assertcore, assert, suite, testing).
	Extension
)

// Module is one bundled module: the bare name a Buzz program imports, its
// provenance Kind, and how to install it on a session. out is std.print's sink;
// modules that do not print ignore it.
type Module struct {
	Name    string
	Kind    Kind
	install func(sess *buzz.Session, out io.Writer)
}

// Modules is the single source of truth for the modules gopherbuzz bundles: the
// upstream-faithful stdlib plus gopherbuzz's own test surface, each tagged with
// its Kind. Register installs every entry; edit this table to add a module.
var Modules = []Module{
	{"std", Upstream, func(s *buzz.Session, out io.Writer) { s.SetSyntheticModule("std", coreModule(out)) }},
	{"math", Upstream, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("math", mathModule()) }},
	{"fs", Upstream, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("fs", fsModule()) }},
	{"os", Upstream, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("os", osModule()) }},
	{"crypto", Upstream, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("crypto", cryptoModule()) }},
	{"gc", Upstream, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("gc", gcModule()) }},
	{"debug", Upstream, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("debug", debugModule()) }},
	{"io", Upstream, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("io", ioModule(s)) }},
	{"serialize", Upstream, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("serialize", serializeModule()) }},
	{"buffer", Upstream, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("buffer", bufferModule()) }},
	{"ffi", Upstream, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("ffi", ffiModule()) }},
	{"assertcore", Extension, func(s *buzz.Session, _ io.Writer) { s.SetSyntheticModule("assertcore", assertCoreModule()) }},
	{"assert", Extension, func(s *buzz.Session, _ io.Writer) { s.SetSourceModule("assert", assertSource) }},
	{"suite", Extension, func(s *buzz.Session, _ io.Writer) { s.SetSourceModule("suite", suiteSource) }},
	{"testing", Extension, func(s *buzz.Session, _ io.Writer) { s.SetSourceModule("testing", testingSource) }},
}

// Register installs every bundled module on sess. std.print writes to os.Stdout;
// use RegisterWithOutput to redirect it.
func Register(sess *buzz.Session) { RegisterWithOutput(sess, os.Stdout) }

// RegisterWithOutput is Register with std.print directed to out. An embedding
// that captures a program's textual output (e.g. the WebAssembly playground)
// passes its own writer so print lands in a buffer instead of the host stdout.
func RegisterWithOutput(sess *buzz.Session, out io.Writer) {
	for _, m := range Modules {
		m.install(sess, out)
	}
}

func fn(name string, f func(context.Context, []vm.Value) (vm.Value, error)) vm.Value {
	return vm.DirectValue(name, f)
}

func mod() vm.Value { return vm.NewMap() }
