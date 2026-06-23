//go:build !(amd64 || arm64) || windows || buzz_safe || buzz_unsafe

package vm

// No JIT backend: jitRun is a no-op, so the caller runs the interpreter.
func (vm *VM) jitRun() (Value, bool, error) { return Null, false, nil }

// JITAvailable reports whether a native JIT backend is compiled in.
func JITAvailable() bool { return false }

// jitArchDefault is read by jit.go's init; no backend ⇒ nothing to enable.
const jitArchDefault = false
