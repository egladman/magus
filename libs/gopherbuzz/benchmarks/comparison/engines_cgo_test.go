//go:build cgo_engines

// Adds the opt-in extended tier - LuaJIT and Umka (both cgo) - to the
// comparison. This file holds no cgo itself (forbidden in _test.go); it drives
// the primitives in luajit_cgo.go / umka_cgo.go. Compiled only under the
// cgo_engines tag:
//
//	GOWORK=off CGO_ENABLED=1 go test -tags cgo_engines -bench=. .
//
// Memory caveat: Go's -benchmem counts only Go-heap allocation. LuaJIT and Umka
// allocate on the C heap, invisible to the Go allocator, so their B/op reads ~0
// and is NOT comparable - read their times only.
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
