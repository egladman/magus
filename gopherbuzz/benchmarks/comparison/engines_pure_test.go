//go:build !cgo_engines

package comparison

// extraEngines contributes no extra engines in the default pure-Go build.
//
// The cgo/native-JIT tier (LuaJIT, Umka) and the wazero WASM tier are gated
// behind the cgo_engines build tag so that the default
//
//	GOWORK=off go test -bench=. .
//
// stays pure Go with no C toolchain or external libraries. Build the full tier
// with:
//
//	GOWORK=off CGO_ENABLED=1 go test -tags cgo_engines -bench=. .
func extraEngines(workload, mode) []namedBench { return nil }
