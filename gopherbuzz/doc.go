// Package buzz is a stack-based bytecode interpreter for the Buzz scripting
// language embedded in magusfiles. It is a Go reimplementation of the upstream
// Buzz language; the language reference lives at
// https://buzz-lang.dev/0.5.0/reference/ .
//
// Architecture: source is lexed and parsed ([Parse]), type-checked,
// then compiled to a flat instruction stream
// ([CompileWith]) that the register-window VM ([VM.Run]) executes.
//
// The primary embedding entry point is [NewSession]; host code injects
// globals with [Session.SetGlobal] and registers target callbacks that Buzz
// can invoke via [Session.Targets].
//
// # Value equality
//
// Equality (==) is structural for scalars and strings but reference-based for
// collections (lists, maps, objects). Two distinct list or map values are
// never == even if their contents match — this avoids O(n) comparison costs
// in the common case. Compare elements explicitly when content equality is
// needed.
package buzz
