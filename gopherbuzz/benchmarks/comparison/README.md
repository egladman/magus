# Cross-language comparison

Benchmarks Buzz against other embedded languages implemented in Go, on two
classic workloads. This is a **separate Go module** (`buzzbench`) so its
comparison dependencies — gopher-lua, tengo, goja — never touch the `gopherbuzz`
module. It uses a `replace` directive to build against the in-tree `gopherbuzz`.

## Run

```sh
cd benchmarks/comparison
# GOWORK=off: this is a separate module, not part of the repo's go.work
GOWORK=off go test -run='^$' -bench=. -benchmem .

# stable medians with confidence intervals
GOWORK=off go test -run='^$' -bench=. -benchmem -count=8 . > out.txt
benchstat out.txt
```

## Workloads

Each engine compiles its program once and executes it per iteration (the same
shape as the in-tree Buzz benchmarks).

- **LoopSum** — sum `0..1e6` in a tight numeric loop. This is the JIT's
  wheelhouse: a top-level numeric loop with no calls.
- **Fib** — recursive `fib(30)`. Call-heavy, so Buzz runs it on the interpreter
  (the JIT does not compile calls yet) — an honest control that measures raw
  interpreter dispatch, not the JIT.

Buzz appears twice (`_BuzzJIT` / `_BuzzInterp`) via `vm.SetJIT`; for `Fib` the
two are ~equal because the recursive path isn't JIT'd.

## Engines

| Bench suffix | Library | Language |
|---|---|---|
| `_Buzz*` | this repo | Buzz |
| `_Lua` | [`yuin/gopher-lua`](https://github.com/yuin/gopher-lua) | Lua 5.1 |
| `_Tengo` | [`d5/tengo`](https://github.com/d5/tengo) | Tengo |
| `_Goja` | [`dop251/goja`](https://github.com/dop251/goja) | JavaScript (ES5.1+) |

## Representative results

benchstat median, n=6, amd64 Xeon @ 2.80 GHz, Go 1.25:

| Engine | LoopSum (1e6) | Fib(30) | LoopSum mem |
|---|--:|--:|--:|
| Buzz (JIT) | **5.9 ms** | 188 ms | 5.7 KB |
| Buzz (interp) | 36.7 ms | **182 ms** | 2.0 KB |
| gopher-lua | 47.6 ms | 266 ms | 15 MB |
| tengo | 78.3 ms | 217 ms | 15 MB |
| goja (JS) | 395 ms | 403 ms | 107 MB |

These are microbenchmarks across languages with different semantics, type
systems, and safety models — read them as order-of-magnitude, not a verdict.
The point of keeping the harness in-tree is that it's easy to add your own
workload and re-measure.
