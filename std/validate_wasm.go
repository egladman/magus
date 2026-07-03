//go:build wasm

package std

// validateModule is a no-op on wasm. The native build (validate.go) reflects over
// each Impl's signature to catch declaration mismatches at init, but TinyGo's wasm
// reflect omits (reflect.Type).NumIn/NumOut and panics if called. The guard is a
// programmer-error check that has already passed on the host before any wasm
// artifact is built, so the browser can safely skip it.
func validateModule(Module) error { return nil }
