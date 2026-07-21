//go:build tinygo

package vm

// memPin is a no-op on TinyGo: it has no runtime.Pinner, its GC does not move
// heap objects (so an ffi.alloc buffer's address is already stable), and the
// WebAssembly playground never calls the C FFI. Native builds use the real
// runtime.Pinner wrapper in ffi_pin.go.
type memPin struct{}

func (m *memPin) Pin(ptr any) {}
func (m *memPin) Unpin()      {}
