# Cross-language comparison

Benchmarks gopherbuzz (this repo's Buzz VM) against other embedded languages, on
ten workloads. This is a **separate Go module** (`buzzbench`) so its comparison
dependencies - gopher-lua, tengo, goja - never touch the `gopherbuzz` module. It
uses a `replace` directive to build against the in-tree `gopherbuzz`.

Two tiers, kept honest by being labelled as such:

- **Pure-Go, no-toolchain** (default): gopherbuzz, gopher-lua, tengo, goja. No
  cgo, no C libraries - what you get from `go test`.
- **Extended tier** (opt-in, `-tags cgo_engines`): LuaJIT (a tracing JIT, cgo)
  and Umka (a C interpreter, cgo). These show the ceiling a JIT/native dependency
  buys, and the gap a pure-Go interpreter accepts in exchange for `CGO_ENABLED=0`,
  cross-compilation, and a tiny Go-managed footprint. See
  [Extended tier](#extended-tier-opt-in).

## A level battlefield

Cross-engine microbenchmarks are easy to skew by accident: if one engine reuses
a warm VM while another rebuilds its VM every iteration, you are no longer
measuring the same thing. To keep every engine on the same footing, each one
runs under **both** of these protocols, and the harness times them identically:

- **Warm** - the VM is constructed once and reused; only repeated execution on
  the warm VM is timed (compilation and VM construction are hoisted out of the
  loop). This is the headline steady-state-throughput number.
- **Fresh** - a new VM is constructed and torn down every iteration, so the
  per-run setup cost is folded in. The compiled program is reused across
  iterations where the engine separates the compiled artifact from VM state
  (gopherbuzz, goja, tengo via `Clone`); for engines whose compiled artifact is bound
  to the VM (gopher-lua), the source is necessarily re-loaded.

For workloads this heavy (`fib(30)` ≈ 10⁶ calls), setup is noise, so Warm ≈ Fresh
on **time** - the axes diverge mainly on **allocations**, where Fresh exposes the
per-run VM allocation that Warm amortizes away.

## Run

```sh
cd benchmarks/comparison
# GOWORK=off: this is a separate module, not part of the repo's go.work
GOWORK=off go test -run='^$' -bench=. -benchmem .

# one workload / one protocol (sub-benchmark names are Workload/Protocol/Engine)
GOWORK=off go test -run='^$' -bench='LoopSum/Warm' -benchmem .

# stable medians with confidence intervals
GOWORK=off go test -run='^$' -bench=. -benchmem -count=6 . > out.txt
benchstat out.txt
```

Sub-benchmark names are `BenchmarkComparison/<Workload>/<Protocol>/<Engine>`,
e.g. `BenchmarkComparison/LoopSum/Warm/Gopherbuzz`. Filter with a regex on any
segment - `-bench='LoopSum/Warm'`, `-bench='/Fresh/Goja'`, etc.

## Workloads

Each program is **self-contained** - it builds whatever data or function it
needs inside the timed program - and every engine runs the same shape. This is
deliberate: the in-tree engine suite (`internal/interp/engine`) can lean on
a persistent session to keep `setup` state alive across a separate `hot` chunk,
but that doesn't port across engines (tengo can't share a defined function or
collection between compiled units), so a setup/hot split would not be level here.
Sizes are picked so the intended operation dominates construction.

- **LoopSum** - sum `0..1e6` in a tight numeric loop. The JIT's wheelhouse: a
  top-level numeric loop with no calls.
- **Fib** - recursive `fib(30)`. Call-heavy, so gopherbuzz runs it on the
  interpreter (the JIT does not compile calls yet) - an honest control that
  measures raw interpreter dispatch, not the JIT.
- **Call** - 1e6 iterations of a trivial two-arg `add` call. LoopSum plus a
  call/return on every iteration, so the delta from LoopSum is call overhead.
- **ForeachList** - build a 1000-element list, then sum it by iteration 1000
  times (1e6 element reads). Stresses list iteration/indexing.
- **ForeachMap** - iterate a 10-entry map's key/value pairs 1e5 times (1e6
  visits). Stresses map iteration and, for some engines, per-iteration key
  enumeration.
- **StringInterp** - build an interpolated/concatenated `"item {i}"` string in a
  1e5-iteration loop.

And four heavier **compute kernels**, to show the whole stack's time _and_
allocation footprint under sustained work:

- **Mandelbrot** - 150×150 escape-time grid, max 100 iterations. Float-heavy
  nested loops, near-zero allocation.
- **MatMul** - 80×80 integer matrix multiply. Nested loops over 2D lists.
- **BinaryTrees** - allocate, walk, and discard ~1M small tree nodes. The
  allocation/GC-pressure workload.
- **NBody** - 5-body gravitational simulation, 1e4 steps, with `sqrt`. Float
  arithmetic and array updates (gopherbuzz runs it via a session so it can
  `import "math"`).

And two **string/text** workloads, which stress substring extraction and map
churn - the area gopherbuzz historically handled worst:

- **KmerCount** - slide a 6-wide window over a ~1 KB string, tally the k-mers in a
  map, 50x.
- **SubstringSearch** - slide over the same string counting a short pattern by
  extracting and comparing each window, 100x (no map).

gopherbuzz is ONE row, `Gopherbuzz`, on every workload. It used to be split into
`GopherbuzzJIT` / `GopherbuzzInterp` via `vm.SetJIT` on the two workloads where the
JIT engages; that distinction is gone, because the JIT is always on and an
interpreter-only configuration is not something a user can select or would ever
run. Compiling is an implementation detail of the engine, not an axis of the
comparison.

## Engines

| Bench engine  | Library                                                 | Language            |
| ------------- | ------------------------------------------------------- | ------------------- |
| `Gopherbuzz*` | this repo                                               | Buzz                |
| `Lua`         | [`yuin/gopher-lua`](https://github.com/yuin/gopher-lua) | Lua 5.1             |
| `Tengo`       | [`d5/tengo`](https://github.com/d5/tengo)               | Tengo               |
| `Goja`        | [`dop251/goja`](https://github.com/dop251/goja)         | JavaScript (ES5.1+) |

## Representative results

benchstat median, n=6, Go 1.26.0, linux/amd64, Intel Xeon @ 2.10 GHz (4 cores),
2026-10-01, at magus commit 7472f16. All four engines (gopherbuzz, gopher-lua,
tengo, goja) were measured in this one run on this one host, so the columns are
directly comparable. Variance on this 4-core cloud host is wider than on a quiet
machine (several cells carry ±10-30% CIs), so read single-digit-percent gaps as
ties.

### Scripting microbenchmarks

**Warm - steady-state execution time** on a reused VM (ms/op, lower is better):

| Engine     | LoopSum | Fib(30) |    Call | ForeachList | ForeachMap | StringInterp |
| ---------- | ------: | ------: | ------: | ----------: | ---------: | -----------: |
| gopherbuzz | **5.2** | **164** |     105 |      **32** |     **44** |         23.2 |
| gopher-lua |    41.9 |     202 | **100** |         116 |        144 |     **21.4** |
| tengo      |    73.3 |     199 |     122 |          53 |        124 |         26.5 |
| goja (JS)  |     366 |     353 |     534 |         493 |        867 |         46.5 |

The LoopSum figure is what a gopherbuzz run does (the JIT is always on); the old
interpreter-only number for it was 40.6. gopherbuzz leads LoopSum, Fib(30),
ForeachList and ForeachMap outright. Call is a tie: gopher-lua's 100 (±12%) and
gopherbuzz's 105 (±10%) are within noise. On `StringInterp` gopher-lua edges it
(21.4 vs 23.2, ±7% and ±8%, so nearly within noise), while gopherbuzz now beats
tengo (26.5) and goja (46.5) - disclosed, not hidden.

**Warm - allocation** (B/op, lower is better):

| Engine     | LoopSum | Fib(30) |   Call | ForeachList | ForeachMap | StringInterp |
| ---------- | ------: | ------: | -----: | ----------: | ---------: | -----------: |
| gopherbuzz |   ~1 KB |  4.1 KB | 2.6 KB |      ~25 KB |    ~1.7 KB |      ~1.5 MB |
| gopher-lua |   15 MB |   88 KB |  31 MB |       23 MB |     9.2 MB |       5.3 MB |
| tengo      |   15 MB |   27 MB |  23 MB |      7.9 MB |      60 MB |        14 MB |
| goja (JS)  |  107 MB |   40 KB | 114 MB |      118 MB |     394 MB |        15 MB |

gopherbuzz's NaN-boxed `[]uint64` stack keeps the numeric/call paths at KB (or,
for warm `LoopSum`, ~1 KB), and `foreach` reuses a per-slot iterator object, so
map/list iteration is nearly allocation-free too (`ForeachMap`'s 1e6 visits cost
~2 KB, not megabytes). `StringInterp` is still gopherbuzz's heaviest scripting
allocation (~1.5 MB), but it is now the lowest of the four engines, and it is
GC-sensitive - its time can still vary from run to run.

### String/text workloads

Two text-processing workloads added to probe gopherbuzz's string handling head-on
(its structural soft spot: every string is content-interned, and substrings churn
the heap). Both are split-free and produce identical results across engines,
guarded by a cross-engine agreement test (`TestExtraStringWorkloadsAgree` in `comparison_test.go`).

- **KmerCount** - slide a 6-wide window over a ~1 KB string, tally the k-mers in a
  map, 50x. Substring extraction + map churn.
- **SubstringSearch** - slide over the same string counting a short pattern by
  extracting each window and comparing, 100x. Substring extraction, no map.

**Warm - execution time** (ms/op) | **allocation** (B/op), lower is better:

| Engine     | KmerCount | KmerCount B/op | SubstringSearch | SubstringSearch B/op |
| ---------- | --------: | -------------: | --------------: | -------------------: |
| gopherbuzz |  **13.0** |     **542 KB** |        **15.5** |             **1 KB** |
| gopher-lua |      18.2 |         2.7 MB |            17.6 |               3.7 MB |
| tengo      |      13.3 |         4.4 MB |            18.4 |               7.2 MB |
| goja (JS)  |        57 |          13 MB |              63 |                12 MB |

These started ~10-18x _behind_ gopher-lua and tengo - and profiling that gap was
the point. It turned up two real bugs and one structural cost, all since fixed:
`str.sub` rebuilt a `[]rune` of the whole string on every call (O(n) per call,
O(n²) over a sliding window); each `s.sub(...)` allocated a fresh bound-method
closure; and every substring appended a new entry to the never-freed global
string-intern heap. With those addressed (an ASCII fast path in `sub`, caching
string-method dispatch in the inline cache, and one cached heap index per
interned string), gopherbuzz now leads the pure-Go field on `SubstringSearch`
(15.5 vs 17.6-18.4), is level with tengo on `KmerCount` (13.0 vs 13.3; tengo's CI
is ±71%), and allocates several times (`KmerCount`) to several thousand times
(`SubstringSearch`) less than every peer. `StringInterp` above is the string
workload where gopher-lua still edges it, by a margin near the noise (21.4 vs
23.2): its strings are all unique, so interning can never amortize them.

### Compute kernels

**Warm - execution time** (ms/op, lower is better):

| Engine     | Mandelbrot | MatMul | BinaryTrees |   NBody |
| ---------- | ---------: | -----: | ----------: | ------: |
| gopherbuzz |     **24** |     62 |         119 | **118** |
| gopher-lua |        228 | **45** |         138 |     132 |
| tengo      |        355 |     67 |      **96** |     123 |
| goja (JS)  |       1863 |    306 |         235 |     592 |

**Warm - allocation** (lower is better):

| Engine     | Mandelbrot |     MatMul | BinaryTrees |     NBody |
| ---------- | ---------: | ---------: | ----------: | --------: |
| gopherbuzz | **~10 KB** | **338 KB** |       32 MB | **27 KB** |
| gopher-lua |      93 MB |     8.5 MB |       45 MB |     25 MB |
| tengo      |     103 MB |      13 MB |   **24 MB** |     27 MB |
| goja (JS)  |     453 MB |      56 MB |      146 MB |     98 MB |

The compute kernels are where the field is most honest. **On Mandelbrot
gopherbuzz leads outright: 24 ms vs gopher-lua's 228, an ~9.5x lead** - the kernel
compiles, because the baseline JIT learned the `and` short-circuit and int->float
promotion, so its nested float loop becomes native SSE code. An earlier run without
compilation measured ~370 ms; that figure was not re-measured (an interpreter-only
configuration is no longer selectable) and is kept only to say what the compiler
is worth. On the other kernels the picture is mixed. gopher-lua keeps MatMul (45
vs 62), though gopherbuzz is now slightly ahead of tengo (67). tengo leads
BinaryTrees (96); gopherbuzz's 119 median is ahead of gopher-lua's 138 but carries
wide variance (±37%: the first three of six samples were slow, the last three
settled at 104-110 ms, and the Fresh protocol measured 104 ±6%). On NBody
gopherbuzz narrowly leads (118 vs tengo's 123, within noise, and gopher-lua's
132).

gopherbuzz's _allocation_ is in a different class on most kernels, and it leads on
Mandelbrot (~10 KB vs 93-453 MB), MatMul (338 KB vs 8.5-56 MB) and NBody (27 KB
vs 25-98 MB) - a tiny, GC-quiet footprint. BinaryTrees is the exception: tengo
allocates less (24 MB vs gopherbuzz's 32 MB median, or 27.5 MB / 492,895 allocs
in the steady-state samples; the slow early samples allocated up to 62 MB),
though gopherbuzz is still below gopher-lua (45 MB) and goja (146 MB). An
earlier run recorded 18 MB here; that does not reproduce at this commit, and the
commit before sessions released their heap values on close measured ~40 MB /
~738k allocs, so 32 MB is not a fresh regression.

### Extended tier (opt-in)

This tier is **off by default**, enabled with a build tag:

```sh
# LuaJIT needs libluajit-5.1-dev (Debian/Ubuntu: apt install libluajit-5.1-dev).
# Umka's C source is vendored (internal/umka, v1.5.6, BSD-2-Clause), built by cgo.
GOWORK=off CGO_ENABLED=1 go test -tags cgo_engines -run='^$' -bench=. -benchmem .
```

- **LuaJIT 2.1** (cgo) - a tracing JIT; reuses the Lua sources verbatim.
- **Umka** (cgo) - a statically typed C interpreter (its own dialect; `workload.umka`).

**Memory:** Go's `-benchmem` counts only Go-heap allocation, so LuaJIT's and
Umka's C-heap usage reads ~0 and is _not_ comparable - read their times only.

Indicative warm times (ms/op). The LuaJIT and Umka columns were NOT re-measured in
the run above (no cgo toolchain on this host); they are from an earlier run on
different hardware and are indicative only. The "best pure-Go" and gopherbuzz
columns are from the new run:

| Workload   | LuaJIT | Umka |     best pure-Go | gopherbuzz |
| ---------- | -----: | ---: | ---------------: | ---------: |
| LoopSum    |    1.5 |   35 | 5.2 (gopherbuzz) |        5.2 |
| Fib(30)    |     24 |  140 | 164 (gopherbuzz) |        164 |
| Call       |    1.2 |   70 | 100 (gopher-lua) |        105 |
| Mandelbrot |    4.9 |  152 |  24 (gopherbuzz) |         24 |
| MatMul     |    0.9 |   37 |  45 (gopher-lua) |         62 |
| NBody      |    1.7 |   60 | 118 (gopherbuzz) |        118 |

These are microbenchmarks across languages with different semantics, type
systems, and safety models - read them as order-of-magnitude, not a verdict.
The point of keeping the harness in-tree is that it's easy to add your own
workload and re-measure.
