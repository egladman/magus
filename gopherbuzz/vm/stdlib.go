package vm

// RegisterStdlib binds the VM-level intrinsic functions into env.
//
// These are the small set of globals that exist at the VM level regardless of
// any module import:
//
//   - zdef(lib, cdecl) → map — bind a C shared library via the FFI provider
//
// Fiber creation uses the & operator (OpFiber). resume/resolve are keyword
// expressions handled by session-bound callables. See session.go.
//
// All other Buzz standard library functions (print, assert, parseInt, toInt,
// math.*, fs.*, os.*, …) are in the magus/buzz/std package and are available
// only after `import "std"`, `import "math"`, etc.  See buzz/std.Register.
func RegisterStdlib(env *Env) {
	env.define("zdef", DirectValue("zdef", builtinZdef))
}
