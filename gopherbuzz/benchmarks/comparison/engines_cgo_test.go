//go:build cgo_engines

// Adds the opt-in extended tier — LuaJIT, Umka (both cgo), and wazero (pure-Go
// WASM JIT) — to the comparison. This file holds no cgo itself (forbidden in
// _test.go); it drives the primitives in luajit_cgo.go / umka_cgo.go / wazero.go.
// Compiled only under the cgo_engines tag:
//
//	GOWORK=off CGO_ENABLED=1 go test -tags cgo_engines -bench=. .
//
// Memory caveat: Go's -benchmem counts only Go-heap allocation. LuaJIT and Umka
// allocate on the C heap, invisible to the Go allocator, so their B/op reads ~0
// and is NOT comparable — read their times only. wazero is pure Go, so its
// -benchmem IS captured and comparable.
package comparison

import "testing"

// extraEngines adds the cgo-backed engines for a workload. LuaJIT reuses the Lua
// source; Umka has its own dialect (workload.umka).
func extraEngines(w workload, m mode) []namedBench {
	var out []namedBench
	if w.lua != "" {
		src := w.lua
		out = append(out, namedBench{"LuaJIT", func(b *testing.B) { benchLuaJIT(b, src, m) }})
	}
	if w.umka != "" {
		src := w.umka
		out = append(out, namedBench{"Umka", func(b *testing.B) { benchUmka(b, src, m) }})
	}
	// wazero runs the compiled WASM kernels (see internal/wasm), available only
	// for the numeric compute workloads.
	if fn := wasmExport[w.name]; fn != "" {
		out = append(out, namedBench{"Wazero", func(b *testing.B) { benchWazero(b, fn, m) }})
	}
	return out
}

// benchLuaJIT runs a Lua chunk on LuaJIT 2.1. Warm reuses one interpreter and a
// registry ref to the compiled chunk; fresh builds a new interpreter per
// iteration (re-loading the source, since the chunk is bound to its state).
func benchLuaJIT(b *testing.B, src string, m mode) {
	b.ReportAllocs()

	if m == warm {
		s := luaNew()
		defer s.close()
		ref, errStr := s.loadRef(src)
		if errStr != "" {
			b.Fatalf("luajit load: %s", errStr)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if e := s.callRef(ref); e != "" {
				b.Fatalf("luajit: %s", e)
			}
		}
		return
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s := luaNew()
		if e := s.loadCall(src); e != "" {
			b.Fatalf("luajit: %s", e)
		}
		s.close()
	}
}

// benchUmka runs an Umka program. Warm compiles once and re-runs the same
// instance (umkaRun is re-runnable); fresh compiles a new instance per iteration.
func benchUmka(b *testing.B, src string, m mode) {
	b.ReportAllocs()

	if m == warm {
		s, errStr := umkaNew(src)
		if errStr != "" {
			b.Fatalf("umka: %s", errStr)
		}
		defer s.free()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if e := s.run(); e != "" {
				b.Fatalf("umka: %s", e)
			}
		}
		return
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s, errStr := umkaNew(src)
		if errStr != "" {
			b.Fatalf("umka: %s", errStr)
		}
		if e := s.run(); e != "" {
			b.Fatalf("umka: %s", e)
		}
		s.free()
	}
}

// wasmExport maps a workload name to the WASM kernel it exports (internal/wasm).
// Only the numeric compute kernels are compiled to WASM.
var wasmExport = map[string]string{"Mandelbrot": "mandelbrot", "MatMul": "matmul", "NBody": "nbody"}

// benchWazero runs a compiled WASM kernel on wazero's compiler (JIT) backend.
// The JIT compile (CompileModule) is hoisted out of the timer in both modes;
// warm reuses one instance, fresh re-instantiates (fresh linear memory) per
// iteration.
func benchWazero(b *testing.B, fn string, m mode) {
	b.ReportAllocs()
	e, err := wazeroNew()
	if err != nil {
		b.Fatalf("wazero compile: %v", err)
	}
	defer e.close()

	if m == warm {
		mod, err := e.instantiate()
		if err != nil {
			b.Fatalf("wazero instantiate: %v", err)
		}
		defer mod.Close(e.ctx)
		f := mod.ExportedFunction(fn)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := f.Call(e.ctx); err != nil {
				b.Fatalf("wazero: %v", err)
			}
		}
		return
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		mod, err := e.instantiate()
		if err != nil {
			b.Fatalf("wazero instantiate: %v", err)
		}
		if _, err := mod.ExportedFunction(fn).Call(e.ctx); err != nil {
			b.Fatalf("wazero: %v", err)
		}
		mod.Close(e.ctx)
	}
}
