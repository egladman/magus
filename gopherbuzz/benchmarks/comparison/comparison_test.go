// Package comparison benchmarks gopherbuzz (this repo's Buzz VM) against other
// embedded languages implemented in Go, on two families of workloads.
//
// Scripting microbenchmarks:
//
//   - LoopSum:      a tight numeric loop summing 0..1e6 (exercises gopherbuzz's JIT).
//   - Fib:          recursive fib(30) (call-heavy; gopherbuzz runs it on the interpreter).
//   - Call:         1e6 trivial function calls in a loop (isolates call/return overhead).
//   - ForeachList:  build a 1000-element list and traverse it 1000 times.
//   - ForeachMap:   iterate a small map's entries 1e5 times.
//   - StringInterp: build an interpolated/concatenated string in a loop.
//
// Compute kernels (heavier, to show the whole stack's time/allocation footprint):
//
//   - Mandelbrot:   150x150 escape-time grid (float-heavy nested loops).
//   - MatMul:       80x80 integer matrix multiply (nested loops + 2D lists).
//   - BinaryTrees:  allocate/walk/discard ~1M tree nodes (GC/allocation-heavy).
//   - NBody:        5-body gravity, 1e4 steps (float arithmetic + sqrt over arrays).
//
// String/text workloads (substring extraction + maps; gopherbuzz's soft spot):
//
//   - KmerCount:       slide a 6-wide window over a ~1 KB string, tally k-mers in a map.
//   - SubstringSearch: slide over the same string counting a short pattern (no map).
//
// The workloads mirror the in-tree engine suite (internal/interp/engine);
// here each is a *self-contained* program (no cross-call shared state), because
// not every engine can persist a defined function or collection across compiled
// units (tengo cannot) - keeping the programs self-contained is what keeps the
// field level. Sizes are chosen so the intended operation dominates construction.
//
// Every engine runs under the same two execution protocols, so the comparison is
// apples-to-apples ("a level battlefield"):
//
//   - Warm:  the VM is constructed once and reused; only repeated execution on
//     the warm VM is timed. This is the headline steady-state-throughput number.
//   - Fresh: a new VM is constructed and torn down every iteration, folding in
//     per-run setup cost. The compiled program is reused across iterations where
//     the engine separates the compiled artifact from VM state (gopherbuzz, goja,
//     tengo via Clone); for gopher-lua, whose compiled artifact is bound to the
//     VM, the source is necessarily re-loaded.
//
// For heavy workloads setup is noise, so Warm ≈ Fresh on time - the axes diverge
// mainly on allocations, where Fresh exposes the per-run VM allocation that Warm
// amortizes away.
//
// This lives in its own module so the comparison dependencies never touch the
// gopherbuzz module.
//
// Run:
//
//	go test -bench . -benchmem ./...
package comparison

import (
	"context"
	"testing"

	tengo "github.com/d5/tengo/v2"
	tengostdlib "github.com/d5/tengo/v2/stdlib"
	"github.com/dop251/goja"
	buzz "github.com/egladman/gopherbuzz"
	buzzstd "github.com/egladman/gopherbuzz/std"
	vmpkg "github.com/egladman/gopherbuzz/vm"
	lua "github.com/yuin/gopher-lua"
)

// mode selects the VM-lifecycle protocol an engine runs under. See the package
// doc for the warm/fresh contract.
type mode int

const (
	warm mode = iota
	fresh
)

var modes = []struct {
	name string
	m    mode
}{{"Warm", warm}, {"Fresh", fresh}}

// workload is one program expressed once per engine dialect (semantically
// equivalent, idiomatic per language). The programs are self-contained: any
// data/function a workload needs is built inside the program.
type workload struct {
	name string
	// jit reports whether gopherbuzz is split into JIT/Interp variants. Only a
	// top-level numeric loop is JIT-eligible (LoopSum); elsewhere the JIT never
	// engages, so a single Gopherbuzz (interpreter) row is reported.
	jit bool
	// session routes gopherbuzz through the shared-globals session path, needed
	// for programs that define named functions (Fib, Call) or import std modules
	// (NBody needs math.sqrt); other workloads use the standalone slot-mode path
	// (which is also what lets the JIT engage).
	session bool
	// bzStd registers the importable std modules (math, …) on the session, so the
	// program can `import "math"`. Only NBody needs it.
	bzStd bool
	// bzSetup is run once on the session (define functions, imports) before timing
	// bzHot; it is empty for slot-mode workloads, where bzHot is the whole program.
	bzSetup, bzHot string
	lua, tengo, js string
	// umka is the workload in Umka, used only by the cgo_engines build. LuaJIT
	// reuses the lua source, so it needs no field of its own.
	umka string
}

// namedBench is one extra engine sub-benchmark contributed by a build-tagged
// file (the cgo engines). The default pure-Go build contributes none.
type namedBench struct {
	name string
	fn   func(*testing.B)
}

var workloads = []workload{
	{
		name: "LoopSum",
		jit:  true,
		bzHot: `var sum = 0; var i = 0;
while (i < 1000000) { sum = sum + i; i = i + 1; } return sum;`,
		lua: `local sum = 0; local i = 0
while i < 1000000 do sum = sum + i; i = i + 1 end
return sum`,
		tengo: `sum := 0
for i := 0; i < 1000000; i++ { sum += i }`,
		js:   `var sum = 0; for (var i = 0; i < 1000000; i++) { sum += i; } sum;`,
		umka: `fn main() { s := 0; for i := 0; i < 1000000; i++ { s += i } }`,
	},
	{
		name:    "Fib",
		session: true,
		// Named `fibo`, not `fib`: gopherbuzz reserves `fib` (upstream-parity
		// keyword), so it cannot be a binding name. Other engines keep `fib`.
		bzSetup: `fun fibo(n: int) > int { if (n <= 1) { return n; } return fibo(n - 1) + fibo(n - 2); }`,
		bzHot:   `fibo(30);`,
		lua: `local function fib(n) if n <= 1 then return n end return fib(n-1) + fib(n-2) end
return fib(30)`,
		tengo: `fib := func(n) { if n <= 1 { return n }; return fib(n-1) + fib(n-2) }
out := fib(30)`,
		js:   `function fib(n){ if (n <= 1) return n; return fib(n-1) + fib(n-2); } fib(30);`,
		umka: "fn fib(n: int): int { if n <= 1 { return n }; return fib(n-1) + fib(n-2) }\nfn main() { fib(30) }",
	},
	{
		name:    "Call",
		session: true,
		bzSetup: `fun add(a: int, b: int) > int { return a + b; }`,
		bzHot:   `var sum = 0; var i = 0; while (i < 1000000) { sum = sum + add(i, 1); i = i + 1; } return sum;`,
		lua: `local function add(a, b) return a + b end
local sum = 0; for i = 0, 999999 do sum = sum + add(i, 1) end
return sum`,
		tengo: `add := func(a, b) { return a + b }
sum := 0
for i := 0; i < 1000000; i++ { sum += add(i, 1) }`,
		js:   `function add(a, b){ return a + b; } var sum = 0; for (var i = 0; i < 1000000; i++) { sum += add(i, 1); } sum;`,
		umka: "fn add(a, b: int): int { return a + b }\nfn main() { s := 0; for i := 0; i < 1000000; i++ { s += add(i, 1) } }",
	},
	{
		name: "ForeachList",
		bzHot: `var items = mut []; var i = 0; while (i < 1000) { items.append(i); i = i + 1; }
var sum = 0; var r = 0; while (r < 1000) { foreach (x in items) { sum = sum + x; } r = r + 1; } return sum;`,
		lua: `local items = {}; for i = 0, 999 do items[#items+1] = i end
local sum = 0; for r = 1, 1000 do for _, x in ipairs(items) do sum = sum + x end end
return sum`,
		tengo: `items := []
for i := 0; i < 1000; i++ { items = append(items, i) }
sum := 0
for r := 0; r < 1000; r++ { for _, x in items { sum += x } }`,
		js: `var items = []; for (var i = 0; i < 1000; i++) { items.push(i); }
var sum = 0; for (var r = 0; r < 1000; r++) { for (var j = 0; j < items.length; j++) { sum += items[j]; } } sum;`,
		umka: `fn main() {
  items := make([]int, 0)
  for i := 0; i < 1000; i++ { items = append(items, i) }
  s := 0
  for r := 0; r < 1000; r++ { for i := 0; i < len(items); i++ { s += items[i] } }
}`,
	},
	{
		name: "ForeachMap",
		bzHot: `final m = {"a":1,"b":2,"c":3,"d":4,"e":5,"f":6,"g":7,"h":8,"i":9,"j":10};
var sum = 0; var r = 0; while (r < 100000) { foreach (k, v in m) { sum = sum + v; } r = r + 1; } return sum;`,
		lua: `local m = {a=1,b=2,c=3,d=4,e=5,f=6,g=7,h=8,i=9,j=10}
local sum = 0; for r = 1, 100000 do for k, v in pairs(m) do sum = sum + v end end
return sum`,
		tengo: `m := {a:1,b:2,c:3,d:4,e:5,f:6,g:7,h:8,i:9,j:10}
sum := 0
for r := 0; r < 100000; r++ { for _, v in m { sum += v } }`,
		js: `var m = {a:1,b:2,c:3,d:4,e:5,f:6,g:7,h:8,i:9,j:10};
var sum = 0; for (var r = 0; r < 100000; r++) { for (var k in m) { sum += m[k]; } } sum;`,
		umka: `fn main() {
  m := map[str]int{}
  m["a"]=1; m["b"]=2; m["c"]=3; m["d"]=4; m["e"]=5; m["f"]=6; m["g"]=7; m["h"]=8; m["i"]=9; m["j"]=10
  s := 0
  for r := 0; r < 100000; r++ { for k in m { s += m[k] } }
}`,
	},
	{
		name:  "StringInterp",
		bzHot: `var s = ""; var i = 0; while (i < 100000) { s = "item {i}"; i = i + 1; } return s;`,
		lua: `local s = ""; local i = 0
while i < 100000 do s = "item "..i; i = i + 1 end
return s`,
		tengo: `s := ""
for i := 0; i < 100000; i++ { s = "item " + string(i) }`,
		js:   `var s = ""; for (var i = 0; i < 100000; i++) { s = "item " + i; } s;`,
		umka: `fn main() { s := ""; for i := 0; i < 100000; i++ { s = sprintf("item %d", i) } }`,
	},
	{
		name: "Mandelbrot", // 150x150 grid, max 100 iters: float-heavy nested loops
		jit:  true,         // JIT-eligible: nested loops, `and` escape, mixed int/float
		bzHot: `var checksum = 0; var py = 0;
while (py < 150) {
  var px = 0;
  while (px < 150) {
    var x0 = px * 0.0125 - 1.5; var y0 = py * 0.01 - 1.0;
    var zx = 0.0; var zy = 0.0; var iter = 0;
    while (iter < 100 and zx * zx + zy * zy <= 4.0) {
      var tmp = zx * zx - zy * zy + x0; zy = 2.0 * zx * zy + y0; zx = tmp; iter = iter + 1;
    }
    checksum = checksum + iter; px = px + 1;
  }
  py = py + 1;
}
return checksum;`,
		lua: `local checksum = 0
for py = 0, 149 do
  for px = 0, 149 do
    local x0 = px * 0.0125 - 1.5; local y0 = py * 0.01 - 1.0
    local zx, zy, iter = 0.0, 0.0, 0
    while iter < 100 and zx*zx + zy*zy <= 4.0 do
      local tmp = zx*zx - zy*zy + x0; zy = 2.0*zx*zy + y0; zx = tmp; iter = iter + 1
    end
    checksum = checksum + iter
  end
end
return checksum`,
		tengo: `checksum := 0
for py := 0; py < 150; py++ {
  for px := 0; px < 150; px++ {
    x0 := float(px)*0.0125 - 1.5; y0 := float(py)*0.01 - 1.0
    zx := 0.0; zy := 0.0; iter := 0
    for iter < 100 && zx*zx + zy*zy <= 4.0 {
      tmp := zx*zx - zy*zy + x0; zy = 2.0*zx*zy + y0; zx = tmp; iter++
    }
    checksum += iter
  }
}`,
		js: `var checksum = 0;
for (var py = 0; py < 150; py++) {
  for (var px = 0; px < 150; px++) {
    var x0 = px*0.0125 - 1.5, y0 = py*0.01 - 1.0;
    var zx = 0.0, zy = 0.0, iter = 0;
    while (iter < 100 && zx*zx + zy*zy <= 4.0) {
      var tmp = zx*zx - zy*zy + x0; zy = 2.0*zx*zy + y0; zx = tmp; iter++;
    }
    checksum += iter;
  }
}
checksum;`,
		umka: `fn main() {
  checksum := 0
  for py := 0; py < 150; py++ {
    for px := 0; px < 150; px++ {
      x0 := real(px)*0.0125 - 1.5; y0 := real(py)*0.01 - 1.0
      zx := 0.0; zy := 0.0; iter := 0
      for iter < 100 && zx*zx + zy*zy <= 4.0 {
        tmp := zx*zx - zy*zy + x0; zy = 2.0*zx*zy + y0; zx = tmp; iter++
      }
      checksum += iter
    }
  }
}`,
	},
	{
		name: "MatMul", // 80x80 integer matrix multiply: nested loops + 2D lists
		bzHot: `var n = 80; var a = mut []; var b = mut []; var i = 0;
while (i < n) {
  var ra = mut []; var rb = mut []; var j = 0;
  while (j < n) { ra.append(i + j); rb.append(i - j); j = j + 1; }
  a.append(ra); b.append(rb); i = i + 1;
}
var trace = 0; i = 0;
while (i < n) {
  var k = 0;
  while (k < n) {
    var s = 0; var j = 0;
    while (j < n) { s = s + a[i][j] * b[j][k]; j = j + 1; }
    if (i == k) { trace = trace + s; }
    k = k + 1;
  }
  i = i + 1;
}
return trace;`,
		lua: `local n = 80; local a, b = {}, {}
for i = 0, n-1 do
  local ra, rb = {}, {}
  for j = 0, n-1 do ra[j] = i + j; rb[j] = i - j end
  a[i] = ra; b[i] = rb
end
local trace = 0
for i = 0, n-1 do
  for k = 0, n-1 do
    local s = 0
    for j = 0, n-1 do s = s + a[i][j] * b[j][k] end
    if i == k then trace = trace + s end
  end
end
return trace`,
		tengo: `n := 80; a := []; b := []
for i := 0; i < n; i++ {
  ra := []; rb := []
  for j := 0; j < n; j++ { ra = append(ra, i+j); rb = append(rb, i-j) }
  a = append(a, ra); b = append(b, rb)
}
trace := 0
for i := 0; i < n; i++ {
  for k := 0; k < n; k++ {
    s := 0
    for j := 0; j < n; j++ { s += a[i][j] * b[j][k] }
    if i == k { trace += s }
  }
}`,
		js: `var n = 80; var a = [], b = [];
for (var i = 0; i < n; i++) {
  var ra = [], rb = [];
  for (var j = 0; j < n; j++) { ra.push(i+j); rb.push(i-j); }
  a.push(ra); b.push(rb);
}
var trace = 0;
for (var i = 0; i < n; i++) {
  for (var k = 0; k < n; k++) {
    var s = 0;
    for (var j = 0; j < n; j++) { s += a[i][j] * b[j][k]; }
    if (i == k) { trace += s; }
  }
}
trace;`,
		umka: `fn main() {
  n := 80
  a := make([][]int, n); b := make([][]int, n)
  for i := 0; i < n; i++ {
    a[i] = make([]int, n); b[i] = make([]int, n)
    for j := 0; j < n; j++ { a[i][j] = i+j; b[i][j] = i-j }
  }
  trace := 0
  for i := 0; i < n; i++ {
    for k := 0; k < n; k++ {
      s := 0
      for j := 0; j < n; j++ { s += a[i][j] * b[j][k] }
      if i == k { trace += s }
    }
  }
}`,
	},
	{
		name:    "BinaryTrees", // allocate/walk/discard ~1M tree nodes: GC- and alloc-heavy
		session: true,
		bzSetup: `fun make(d: int) > any { if (d <= 0) { return null; } return [make(d - 1), make(d - 1)]; }
fun check(n: any) > int { if (n == null) { return 1; } return 1 + check(n[0]) + check(n[1]); }`,
		bzHot: `var total = 0; var i = 0; while (i < 30) { total = total + check(make(13)); i = i + 1; } return total;`,
		lua: `local function make(d) if d <= 0 then return nil end return {make(d-1), make(d-1)} end
local function check(n) if n == nil then return 1 end return 1 + check(n[1]) + check(n[2]) end
local total = 0
for i = 1, 30 do total = total + check(make(13)) end
return total`,
		tengo: `make := func(d) { if d <= 0 { return undefined }; return [make(d-1), make(d-1)] }
check := func(n) { if n == undefined { return 1 }; return 1 + check(n[0]) + check(n[1]) }
total := 0
for i := 0; i < 30; i++ { total += check(make(13)) }`,
		js: `function make(d){ if (d <= 0) return null; return [make(d-1), make(d-1)]; }
function check(n){ if (n === null) return 1; return 1 + check(n[0]) + check(n[1]); }
var total = 0; for (var i = 0; i < 30; i++) { total += check(make(13)); } total;`,
		umka: `type Node = struct { l, r: ^Node }
fn makeTree(d: int): ^Node { if d <= 0 { return null }; t := new(Node); t.l = makeTree(d-1); t.r = makeTree(d-1); return t }
fn check(t: ^Node): int { if t == null { return 1 }; return 1 + check(t.l) + check(t.r) }
fn main() { total := 0; for i := 0; i < 30; i++ { total += check(makeTree(13)) } }`,
	},
	{
		name:    "NBody", // 5-body gravity, 5e4 steps: float arithmetic + sqrt over arrays
		session: true,
		bzStd:   true,
		bzSetup: `import "math";`,
		bzHot: `var n = 5;
var x = mut []; var y = mut []; var z = mut [];
var vx = mut []; var vy = mut []; var vz = mut []; var m = mut [];
var i = 0;
while (i < n) {
  x.append(i * 1.0); y.append(i * 0.5); z.append(i * 0.25);
  vx.append(0.0); vy.append(0.0); vz.append(0.0); m.append(i + 1.0);
  i = i + 1;
}
var dt = 0.01; var step = 0;
while (step < 10000) {
  i = 0;
  while (i < n) {
    var j = i + 1;
    while (j < n) {
      var dx = x[i] - x[j]; var dy = y[i] - y[j]; var dz = z[i] - z[j];
      var d2 = dx * dx + dy * dy + dz * dz; var dist = math.sqrt(d2); var mag = dt / (d2 * dist);
      vx[i] = vx[i] - dx * m[j] * mag; vy[i] = vy[i] - dy * m[j] * mag; vz[i] = vz[i] - dz * m[j] * mag;
      vx[j] = vx[j] + dx * m[i] * mag; vy[j] = vy[j] + dy * m[i] * mag; vz[j] = vz[j] + dz * m[i] * mag;
      j = j + 1;
    }
    i = i + 1;
  }
  i = 0;
  while (i < n) { x[i] = x[i] + vx[i] * dt; y[i] = y[i] + vy[i] * dt; z[i] = z[i] + vz[i] * dt; i = i + 1; }
  step = step + 1;
}
var e = 0.0; i = 0;
while (i < n) { e = e + 0.5 * m[i] * (vx[i] * vx[i] + vy[i] * vy[i] + vz[i] * vz[i]); i = i + 1; }
return e;`,
		lua: `local n = 5
local x, y, z, vx, vy, vz, m = {}, {}, {}, {}, {}, {}, {}
for i = 0, n-1 do
  x[i]=i*1.0; y[i]=i*0.5; z[i]=i*0.25; vx[i]=0.0; vy[i]=0.0; vz[i]=0.0; m[i]=i+1.0
end
local dt = 0.01
for step = 1, 10000 do
  for i = 0, n-1 do
    for j = i+1, n-1 do
      local dx, dy, dz = x[i]-x[j], y[i]-y[j], z[i]-z[j]
      local d2 = dx*dx + dy*dy + dz*dz
      local dist = math.sqrt(d2)
      local mag = dt / (d2 * dist)
      vx[i]=vx[i]-dx*m[j]*mag; vy[i]=vy[i]-dy*m[j]*mag; vz[i]=vz[i]-dz*m[j]*mag
      vx[j]=vx[j]+dx*m[i]*mag; vy[j]=vy[j]+dy*m[i]*mag; vz[j]=vz[j]+dz*m[i]*mag
    end
  end
  for i = 0, n-1 do x[i]=x[i]+vx[i]*dt; y[i]=y[i]+vy[i]*dt; z[i]=z[i]+vz[i]*dt end
end
local e = 0.0
for i = 0, n-1 do e = e + 0.5*m[i]*(vx[i]*vx[i]+vy[i]*vy[i]+vz[i]*vz[i]) end
return e`,
		tengo: `math := import("math")
n := 5
x := []; y := []; z := []; vx := []; vy := []; vz := []; m := []
for i := 0; i < n; i++ {
  x = append(x, float(i)*1.0); y = append(y, float(i)*0.5); z = append(z, float(i)*0.25)
  vx = append(vx, 0.0); vy = append(vy, 0.0); vz = append(vz, 0.0); m = append(m, float(i)+1.0)
}
dt := 0.01
for step := 0; step < 10000; step++ {
  for i := 0; i < n; i++ {
    for j := i+1; j < n; j++ {
      dx := x[i]-x[j]; dy := y[i]-y[j]; dz := z[i]-z[j]
      d2 := dx*dx + dy*dy + dz*dz; dist := math.sqrt(d2); mag := dt / (d2 * dist)
      vx[i] = vx[i]-dx*m[j]*mag; vy[i] = vy[i]-dy*m[j]*mag; vz[i] = vz[i]-dz*m[j]*mag
      vx[j] = vx[j]+dx*m[i]*mag; vy[j] = vy[j]+dy*m[i]*mag; vz[j] = vz[j]+dz*m[i]*mag
    }
  }
  for i := 0; i < n; i++ { x[i] = x[i]+vx[i]*dt; y[i] = y[i]+vy[i]*dt; z[i] = z[i]+vz[i]*dt }
}
e := 0.0
for i := 0; i < n; i++ { e += 0.5*m[i]*(vx[i]*vx[i]+vy[i]*vy[i]+vz[i]*vz[i]) }`,
		js: `var n = 5;
var x=[], y=[], z=[], vx=[], vy=[], vz=[], m=[];
for (var i = 0; i < n; i++) { x[i]=i*1.0; y[i]=i*0.5; z[i]=i*0.25; vx[i]=0.0; vy[i]=0.0; vz[i]=0.0; m[i]=i+1.0; }
var dt = 0.01;
for (var step = 0; step < 10000; step++) {
  for (var i = 0; i < n; i++) {
    for (var j = i+1; j < n; j++) {
      var dx=x[i]-x[j], dy=y[i]-y[j], dz=z[i]-z[j];
      var d2 = dx*dx+dy*dy+dz*dz, dist = Math.sqrt(d2), mag = dt/(d2*dist);
      vx[i]-=dx*m[j]*mag; vy[i]-=dy*m[j]*mag; vz[i]-=dz*m[j]*mag;
      vx[j]+=dx*m[i]*mag; vy[j]+=dy*m[i]*mag; vz[j]+=dz*m[i]*mag;
    }
  }
  for (var i = 0; i < n; i++) { x[i]+=vx[i]*dt; y[i]+=vy[i]*dt; z[i]+=vz[i]*dt; }
}
var e = 0.0;
for (var i = 0; i < n; i++) { e += 0.5*m[i]*(vx[i]*vx[i]+vy[i]*vy[i]+vz[i]*vz[i]); }
e;`,
		umka: `fn main() {
  n := 5
  x := make([]real, n); y := make([]real, n); z := make([]real, n)
  vx := make([]real, n); vy := make([]real, n); vz := make([]real, n); m := make([]real, n)
  for i := 0; i < n; i++ {
    x[i]=real(i); y[i]=real(i)*0.5; z[i]=real(i)*0.25
    vx[i]=0.0; vy[i]=0.0; vz[i]=0.0; m[i]=real(i)+1.0
  }
  dt := 0.01
  for step := 0; step < 10000; step++ {
    for i := 0; i < n; i++ {
      for j := i+1; j < n; j++ {
        dx := x[i]-x[j]; dy := y[i]-y[j]; dz := z[i]-z[j]
        d2 := dx*dx + dy*dy + dz*dz; dist := sqrt(d2); mag := dt/(d2*dist)
        vx[i]-=dx*m[j]*mag; vy[i]-=dy*m[j]*mag; vz[i]-=dz*m[j]*mag
        vx[j]+=dx*m[i]*mag; vy[j]+=dy*m[i]*mag; vz[j]+=dz*m[i]*mag
      }
    }
    for i := 0; i < n; i++ { x[i]+=vx[i]*dt; y[i]+=vy[i]*dt; z[i]+=vz[i]*dt }
  }
  e := 0.0
  for i := 0; i < n; i++ { e += 0.5*m[i]*(vx[i]*vx[i]+vy[i]*vy[i]+vz[i]*vz[i]) }
}`,
	},
	{
		// KmerCount slides a 6-wide window over a ~2 KB string and tallies the
		// k-mers in a map, 500 times. It is deliberately a workload gopherbuzz does
		// NOT win: every s.sub() allocates and content-interns a fresh substring
		// (gopherbuzz interns all strings into a never-cleared global table), and the
		// map is rebuilt each pass. A canonical k-nucleotide-style shape - the field
		// here is string-handling, gopherbuzz's structural soft spot.
		name: "KmerCount",
		bzHot: `var base = "ACGTTGCAATGCCAGTACGATCGTTAGCATCGGATCCGATTACGGCATAGCTAGCTAGGCAACT";
var s = base.repeat(16); final L = s.len(); var total = 0; var r = 0;
while (r < 50) {
  var m = mut {}; var i = 0;
  while (i < L - 5) {
    final key = s.sub(i, len: 6); final c = m[key];
    if (c == null) { m[key] = 1; } else { m[key] = c + 1; }
    i = i + 1;
  }
  total = total + m.size(); r = r + 1;
}
return total;`,
		lua: `local base = "ACGTTGCAATGCCAGTACGATCGTTAGCATCGGATCCGATTACGGCATAGCTAGCTAGGCAACT"
local s = base:rep(16); local L = #s; local total = 0
for r = 1, 50 do
  local m = {}
  for i = 1, L - 5 do
    local key = s:sub(i, i + 5); m[key] = (m[key] or 0) + 1
  end
  local d = 0; for _ in pairs(m) do d = d + 1 end
  total = total + d
end
return total`,
		tengo: `base := "ACGTTGCAATGCCAGTACGATCGTTAGCATCGGATCCGATTACGGCATAGCTAGCTAGGCAACT"
s := ""
for i := 0; i < 16; i++ { s += base }
L := len(s); total := 0
for r := 0; r < 50; r++ {
  m := {}
  for i := 0; i < L - 5; i++ {
    key := s[i:i+6]
    if v := m[key]; v == undefined { m[key] = 1 } else { m[key] = v + 1 }
  }
  total += len(m)
}`,
		js: `var base = "ACGTTGCAATGCCAGTACGATCGTTAGCATCGGATCCGATTACGGCATAGCTAGCTAGGCAACT";
var s = base.repeat(16); var L = s.length; var total = 0;
for (var r = 0; r < 50; r++) {
  var m = {};
  for (var i = 0; i < L - 5; i++) {
    var key = s.substr(i, 6); var c = m[key];
    if (c === undefined) { m[key] = 1; } else { m[key] = c + 1; }
  }
  total += Object.keys(m).length;
}
total;`,
	},
	{
		// SubstringSearch slides over the same ~2 KB string counting occurrences of a
		// short pattern by extracting each window and comparing, 800 times. No map -
		// it isolates pure substring extraction (allocate + intern per window). Like
		// KmerCount, this is chosen to show gopherbuzz's string cost honestly, not to
		// flatter it.
		name: "SubstringSearch",
		bzHot: `var base = "ACGTTGCAATGCCAGTACGATCGTTAGCATCGGATCCGATTACGGCATAGCTAGCTAGGCAACT";
var s = base.repeat(16); final L = s.len(); final needle = "GCA"; var count = 0; var r = 0;
while (r < 100) {
  var i = 0;
  while (i < L - 2) {
    if (s.sub(i, len: 3) == needle) { count = count + 1; }
    i = i + 1;
  }
  r = r + 1;
}
return count;`,
		lua: `local base = "ACGTTGCAATGCCAGTACGATCGTTAGCATCGGATCCGATTACGGCATAGCTAGCTAGGCAACT"
local s = base:rep(16); local L = #s; local pat = "GCA"; local count = 0
for r = 1, 100 do
  for i = 1, L - 2 do
    if s:sub(i, i + 2) == pat then count = count + 1 end
  end
end
return count`,
		tengo: `base := "ACGTTGCAATGCCAGTACGATCGTTAGCATCGGATCCGATTACGGCATAGCTAGCTAGGCAACT"
s := ""
for i := 0; i < 16; i++ { s += base }
L := len(s); pat := "GCA"; count := 0
for r := 0; r < 100; r++ {
  for i := 0; i < L - 2; i++ {
    if s[i:i+3] == pat { count++ }
  }
}`,
		js: `var base = "ACGTTGCAATGCCAGTACGATCGTTAGCATCGGATCCGATTACGGCATAGCTAGCTAGGCAACT";
var s = base.repeat(16); var L = s.length; var pat = "GCA"; var count = 0;
for (var r = 0; r < 100; r++) {
  for (var i = 0; i < L - 2; i++) {
    if (s.substr(i, 3) === pat) { count++; }
  }
}
count;`,
	},
}

// BenchmarkComparison runs every workload through every engine under both
// protocols, producing names like
// BenchmarkComparison/LoopSum/Warm/GopherbuzzJIT and
// BenchmarkComparison/Fib/Fresh/Lua. Compare engines at a fixed workload and
// protocol with e.g. `benchstat` filtered on `-bench=LoopSum/Warm`.
func BenchmarkComparison(b *testing.B) {
	for _, w := range workloads {
		b.Run(w.name, func(b *testing.B) {
			for _, mo := range modes {
				b.Run(mo.name, func(b *testing.B) {
					runBuzz := func(b *testing.B, jit bool) {
						if w.session {
							benchBuzzSession(b, jit, mo.m, w.bzStd, w.bzSetup, w.bzHot)
							return
						}
						benchBuzzSlot(b, jit, mo.m, w.bzHot)
					}
					if w.jit {
						b.Run("GopherbuzzJIT", func(b *testing.B) { runBuzz(b, true) })
						b.Run("GopherbuzzInterp", func(b *testing.B) { runBuzz(b, false) })
					} else {
						b.Run("Gopherbuzz", func(b *testing.B) { runBuzz(b, false) })
					}
					b.Run("Lua", func(b *testing.B) { benchLua(b, w.lua, mo.m) })
					b.Run("Tengo", func(b *testing.B) { benchTengo(b, w.tengo, mo.m) })
					b.Run("Goja", func(b *testing.B) { benchGoja(b, w.name+".js", w.js, mo.m) })
					// Engines that need a C toolchain (LuaJIT, Umka) opt in via
					// extraEngines, which is empty in the default pure-Go build.
					// See engines_pure_test.go / engines_cgo_test.go.
					for _, e := range extraEngines(w, mo.m) {
						b.Run(e.name, e.fn)
					}
				})
			}
		})
	}
}

// ── gopherbuzz ───────────────────────────────────────────────────────────────

// benchBuzzSlot runs a self-contained top-level chunk on the standalone
// slot-mode path (JIT-eligible). The chunk is compiled once; warm reuses one VM,
// fresh builds a new VM per iteration.
func benchBuzzSlot(b *testing.B, jit bool, m mode, program string) {
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
	vmpkg.SetJIT(jit)
	defer vmpkg.SetJIT(true)
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
// shared-globals path magus uses), then times the hot chunk. The recursive Fib
// call path is not JIT'd yet, so this runs on the interpreter regardless of the
// JIT flag - an honest control.
//
// Warm reuses one session and times only the hot chunk; fresh stands up a new
// session (define + compile + run) per iteration, the honest cost of a cold run.
func benchBuzzSession(b *testing.B, jit bool, m mode, useStd bool, setup, hot string) {
	vmpkg.SetJIT(jit)
	defer vmpkg.SetJIT(true)
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
