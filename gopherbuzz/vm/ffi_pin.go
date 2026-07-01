//go:build !tinygo

package vm

import "runtime"

// memPin keeps an ffi.alloc buffer at a fixed address while a C callee may hold
// it. On native (and standard js/wasm) targets it wraps runtime.Pinner, so Go's
// moving GC cannot relocate the backing slice between the Pin and the Unpin.
//
// TinyGo has no runtime.Pinner; its build uses the no-op variant in
// ffi_pin_tinygo.go (its GC does not move objects, and the wasm playground never
// calls the C FFI), which is what keeps the WebAssembly playground buildable.
type memPin struct{ p runtime.Pinner }

func (m *memPin) Pin(ptr any) { m.p.Pin(ptr) }
func (m *memPin) Unpin()      { m.p.Unpin() }
