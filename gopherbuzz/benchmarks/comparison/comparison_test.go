// Package comparison benchmarks Buzz against other embedded languages
// implemented in Go, on two classic workloads:
//
//   - LoopSum: a tight numeric loop summing 0..1e6 (exercises Buzz's JIT).
//   - Fib:     recursive fib(30) (call-heavy; Buzz does not JIT calls yet, so
//     this measures the interpreter and is an honest control).
//
// Each engine compiles its program once and executes it per b.N iteration, the
// same shape as the in-tree Buzz benchmarks. This lives in its own module so the
// comparison dependencies never touch the gopherbuzz module.
//
// Run:
//
//	go test -bench . -benchmem ./...
package comparison

import (
	"context"
	"testing"

	tengo "github.com/d5/tengo/v2"
	"github.com/dop251/goja"
	buzz "github.com/egladman/gopherbuzz"
	vmpkg "github.com/egladman/gopherbuzz/vm"
	lua "github.com/yuin/gopher-lua"
)

// ── source programs (semantically identical across languages) ────────────────

const (
	buzzLoop = `var sum = 0; var i = 0;
while (i < 1000000) { sum = sum + i; i = i + 1; } return sum;`
	buzzFib = `fun fib(n) int { if (n <= 1) { return n; } return fib(n - 1) + fib(n - 2); }`

	luaLoop = `local sum = 0; local i = 0
while i < 1000000 do sum = sum + i; i = i + 1 end
return sum`
	luaFib = `local function fib(n) if n <= 1 then return n end return fib(n-1) + fib(n-2) end
return fib(30)`

	tengoLoop = `sum := 0
for i := 0; i < 1000000; i++ { sum += i }`
	tengoFib = `fib := func(n) { if n <= 1 { return n }; return fib(n-1) + fib(n-2) }
out := fib(30)`

	jsLoop = `var sum = 0; for (var i = 0; i < 1000000; i++) { sum += i; } sum;`
	jsFib  = `function fib(n){ if (n <= 1) return n; return fib(n-1) + fib(n-2); } fib(30);`
)

// ── Buzz ─────────────────────────────────────────────────────────────────────

// benchBuzzLoop runs a slot-mode top-level chunk (JIT-eligible).
func benchBuzzLoop(b *testing.B, jit bool) {
	prog, err := buzz.Parse(buzzLoop)
	if err != nil {
		b.Fatalf("parse: %v", err)
	}
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	env := vmpkg.NewEnv()
	vmpkg.RegisterStdlib(env)
	vmpkg.SetJIT(jit)
	defer vmpkg.SetJIT(true)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vmpkg.NewVM(ctx).Run(chunk, env); err != nil {
			b.Fatal(err)
		}
	}
}

// benchBuzzFib defines fib as a session global (so its recursive self-reference
// resolves) and runs fib(30). The recursive call path is not JIT'd yet, so this
// runs on the interpreter regardless of the JIT flag — an honest control.
func benchBuzzFib(b *testing.B, jit bool) {
	vmpkg.SetJIT(jit)
	defer vmpkg.SetJIT(true)
	ctx := context.Background()
	sess := buzz.NewSession(ctx)
	defer sess.Close()
	if err := sess.Exec(ctx, buzzFib); err != nil {
		b.Fatalf("define: %v", err)
	}
	chunk, err := sess.Compile(`fib(30);`)
	if err != nil {
		b.Fatalf("compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := sess.ExecChunk(ctx, chunk); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoopSum_BuzzJIT(b *testing.B)    { benchBuzzLoop(b, true) }
func BenchmarkLoopSum_BuzzInterp(b *testing.B) { benchBuzzLoop(b, false) }
func BenchmarkFib_BuzzJIT(b *testing.B)        { benchBuzzFib(b, true) }
func BenchmarkFib_BuzzInterp(b *testing.B)     { benchBuzzFib(b, false) }

// ── gopher-lua ───────────────────────────────────────────────────────────────

func benchLua(b *testing.B, src string) {
	L := lua.NewState()
	defer L.Close()
	fn, err := L.LoadString(src)
	if err != nil {
		b.Fatalf("lua load: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		L.Push(fn)
		if err := L.PCall(0, lua.MultRet, nil); err != nil {
			b.Fatal(err)
		}
		L.SetTop(0)
	}
}

func BenchmarkLoopSum_Lua(b *testing.B) { benchLua(b, luaLoop) }
func BenchmarkFib_Lua(b *testing.B)     { benchLua(b, luaFib) }

// ── tengo ────────────────────────────────────────────────────────────────────

func benchTengo(b *testing.B, src string) {
	compiled, err := tengo.NewScript([]byte(src)).Compile()
	if err != nil {
		b.Fatalf("tengo compile: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := compiled.Clone().Run(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoopSum_Tengo(b *testing.B) { benchTengo(b, tengoLoop) }
func BenchmarkFib_Tengo(b *testing.B)     { benchTengo(b, tengoFib) }

// ── goja (JavaScript) ────────────────────────────────────────────────────────

func benchGoja(b *testing.B, name, src string) {
	prog, err := goja.Compile(name, src, false)
	if err != nil {
		b.Fatalf("goja compile: %v", err)
	}
	vm := goja.New()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := vm.RunProgram(prog); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLoopSum_Goja(b *testing.B) { benchGoja(b, "loop.js", jsLoop) }
func BenchmarkFib_Goja(b *testing.B)     { benchGoja(b, "fib.js", jsFib) }
