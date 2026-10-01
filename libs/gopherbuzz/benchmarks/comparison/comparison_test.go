package comparison

import (
	"context"
	"testing"

	tengo "github.com/d5/tengo/v2"
	tengostdlib "github.com/d5/tengo/v2/stdlib"
	"github.com/dop251/goja"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	vmpkg "github.com/egladman/magus/libs/gopherbuzz/vm"
	lua "github.com/yuin/gopher-lua"
)

// BenchmarkComparison runs every workload through every engine under both
// protocols, producing names like BenchmarkComparison/LoopSum/Warm/Gopherbuzz and
// BenchmarkComparison/Fib/Fresh/Lua. Compare engines at a fixed workload and
// protocol with e.g. `benchstat` filtered on `-bench=LoopSum/Warm`.
func BenchmarkComparison(b *testing.B) {
	for _, w := range workloads {
		b.Run(w.name, func(b *testing.B) {
			for _, mo := range modes {
				b.Run(mo.name, func(b *testing.B) {
					b.Run("Gopherbuzz", func(b *testing.B) {
						if w.session {
							benchBuzzSession(b, mo.m, w.bzStd, w.bzSetup, w.bzHot)
							return
						}
						benchBuzzSlot(b, mo.m, w.bzHot)
					})
					b.Run("Lua", func(b *testing.B) { benchLua(b, w.lua, mo.m) })
					b.Run("Tengo", func(b *testing.B) { benchTengo(b, w.tengo, mo.m) })
					b.Run("Goja", func(b *testing.B) { benchGoja(b, w.name+".js", w.js, mo.m) })
					// Engines that need a C toolchain (LuaJIT, Umka) opt in via
					// extraEngines, which is empty in the default pure-Go build.
					// See engines_pure.go / engines_cgo.go.
					for _, e := range extraEngines(w, mo.m) {
						b.Run(e.name, e.fn)
					}
				})
			}
		})
	}
}

// ── gopherbuzz ───────────────────────────────────────────────────────────────

// benchBuzzSlot runs a self-contained top-level chunk on the standalone slot-mode
// path. The chunk is compiled once; warm reuses one VM, fresh builds a new VM per
// iteration.
func benchBuzzSlot(b *testing.B, m mode, program string) {
	prog, err := buzz.ParseEmbedded(program)
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	env := vmpkg.NewEnv()
	vmpkg.RegisterStdlib(env)
	ctx := context.Background()
	b.ReportAllocs()

	if m == warm {
		vm := vmpkg.NewVM(ctx)
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := vm.Run(chunk, env); err != nil {
				b.Fatal(err)
			}
		}
		return
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vmpkg.NewVM(ctx).Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// benchBuzzSession runs setup once to define named functions on a session (the
// shared-globals path magus uses), then times the hot chunk.
//
// Warm reuses one session and times only the hot chunk; fresh stands up a new
// session (define + compile + run) per iteration, the honest cost of a cold run.
func benchBuzzSession(b *testing.B, m mode, useStd bool, setup, hot string) {
	ctx := context.Background()
	b.ReportAllocs()

	newSess := func() *buzz.Session {
		sess := buzz.NewSession(ctx, buzz.WithEmbedded())
		if useStd {
			buzzstd.Register(sess) // enable `import "math"`, etc.
		}
		return sess
	}

	if m == warm {
		sess := newSess()
		defer sess.Close()
		if err := sess.Exec(ctx, setup); err != nil {
			b.Fatalf("define: %v", err)
		}
		chunk, err := sess.Compile(hot)
		if err != nil {
			b.Fatalf("compile: %v", err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if err := sess.ExecChunk(ctx, chunk); err != nil {
				b.Fatal(err)
			}
		}
		return
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sess := newSess()
		if err := sess.Exec(ctx, setup); err != nil {
			b.Fatal(err)
		}
		chunk, err := sess.Compile(hot)
		if err != nil {
			b.Fatal(err)
		}
		if err := sess.ExecChunk(ctx, chunk); err != nil {
			b.Fatal(err)
		}
		sess.Close()
	}
}

// ── gopher-lua ───────────────────────────────────────────────────────────────

// benchLua warm reuses one LState (loading the chunk once); fresh builds a new
// LState per iteration. A loaded function is bound to its LState, so the fresh
// path necessarily re-loads (compiles) the source - there is no way to carry a
// compiled chunk onto a fresh state.
func benchLua(b *testing.B, src string, m mode) {
	b.ReportAllocs()

	if m == warm {
		L := lua.NewState()
		defer L.Close()
		fn, err := L.LoadString(src)
		if err != nil {
			b.Fatalf("lua load: %v", err)
		}
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			L.Push(fn)
			if err := L.PCall(0, lua.MultRet, nil); err != nil {
				b.Fatal(err)
			}
			L.SetTop(0)
		}
		return
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		L := lua.NewState()
		fn, err := L.LoadString(src)
		if err != nil {
			b.Fatal(err)
		}
		L.Push(fn)
		if err := L.PCall(0, lua.MultRet, nil); err != nil {
			b.Fatal(err)
		}
		L.Close()
	}
}

// ── tengo ────────────────────────────────────────────────────────────────────

// benchTengo compiles once. tengo's Compiled.Run constructs its VM internally
// on every call, so warm (Run on the shared Compiled) and fresh (Run on a
// Clone) differ only by the Clone's per-iteration copy of the globals.
func benchTengo(b *testing.B, src string, m mode) {
	script := tengo.NewScript([]byte(src))
	script.SetImports(tengostdlib.GetModuleMap("math")) // NBody does import("math")
	compiled, err := script.Compile()
	if err != nil {
		b.Fatalf("tengo compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c := compiled
		if m == fresh {
			c = compiled.Clone()
		}
		if err := c.Run(); err != nil {
			b.Fatal(err)
		}
	}
}

// ── goja (JavaScript) ────────────────────────────────────────────────────────

// benchGoja compiles the program once; warm reuses one Runtime, fresh builds a
// new Runtime per iteration.
func benchGoja(b *testing.B, name, src string, m mode) {
	prog, err := goja.Compile(name, src, false)
	if err != nil {
		b.Fatalf("goja compile: %v", err)
	}
	b.ReportAllocs()

	if m == warm {
		vm := goja.New()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := vm.RunProgram(prog); err != nil {
				b.Fatal(err)
			}
		}
		return
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := goja.New().RunProgram(prog); err != nil {
			b.Fatal(err)
		}
	}
}

// TestExtraStringWorkloadsAgree guards the honest-comparison string workloads
// (KmerCount, SubstringSearch): a cross-language benchmark only means something
// if every engine computes the same answer, so this runs each engine's program
// once and asserts they agree before any timing is trusted.
func TestExtraStringWorkloadsAgree(t *testing.T) {
	cases := []struct{ name, tengoVar string }{
		{"KmerCount", "total"},
		{"SubstringSearch", "count"},
	}
	for _, tc := range cases {
		w := workloadByName(t, tc.name)
		bz := buzzResult(t, w.bzHot)
		lv := luaResult(t, w.lua)
		tg := tengoResult(t, w.tengo, tc.tengoVar)
		js := gojaResult(t, w.js)
		if bz != lv || bz != tg || bz != js {
			t.Errorf("%s engines disagree: buzz=%d lua=%d tengo=%d goja=%d", tc.name, bz, lv, tg, js)
			continue
		}
		t.Logf("%s = %d (buzz == lua == tengo == goja)", tc.name, bz)
	}
}

func workloadByName(t *testing.T, name string) workload {
	t.Helper()
	for _, w := range workloads {
		if w.name == name {
			return w
		}
	}
	t.Fatalf("workload %q not found", name)
	return workload{}
}

func buzzResult(t *testing.T, program string) int64 {
	t.Helper()
	prog, err := buzz.ParseEmbedded(program)
	if err != nil {
		t.Fatalf("buzz parse: %v", err)
	}
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	if err != nil {
		t.Fatalf("buzz compile: %v", err)
	}
	env := vmpkg.NewEnv()
	vmpkg.RegisterStdlib(env)
	v, err := vmpkg.NewVM(context.Background()).Run(chunk, env)
	if err != nil {
		t.Fatalf("buzz run: %v", err)
	}
	return v.AsInt()
}

func luaResult(t *testing.T, src string) int64 {
	t.Helper()
	L := lua.NewState()
	defer L.Close()
	fn, err := L.LoadString(src)
	if err != nil {
		t.Fatalf("lua load: %v", err)
	}
	L.Push(fn)
	if err := L.PCall(0, 1, nil); err != nil {
		t.Fatalf("lua call: %v", err)
	}
	r := L.Get(-1)
	L.Pop(1)
	return int64(lua.LVAsNumber(r))
}

func tengoResult(t *testing.T, src, varName string) int64 {
	t.Helper()
	s := tengo.NewScript([]byte(src))
	s.SetImports(tengostdlib.GetModuleMap("math"))
	c, err := s.Run()
	if err != nil {
		t.Fatalf("tengo run: %v", err)
	}
	return c.Get(varName).Int64()
}

func gojaResult(t *testing.T, src string) int64 {
	t.Helper()
	v, err := goja.New().RunString(src)
	if err != nil {
		t.Fatalf("goja run: %v", err)
	}
	return v.ToInteger()
}
