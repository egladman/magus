# gopherbuzz benchmarks

Microbenchmarks for the gopherbuzz VM. The benchmark code lives in `bench_test.go`
(and `vm/*_test.go`); this directory holds the optimization analysis and the
cross-language comparison. Result dumps are not committed - regenerate them.

## Run

```sh
# all gopherbuzz benchmarks (from the gopherbuzz module root)
go test -run='^$' -bench=. -benchmem ./...

# one benchmark
go test -run='^$' -bench=BenchmarkLoopSum -benchmem .
```

Benchmarks: `Fib`, `LoopSum`, `LoopSumFloat`, `LoopEq`, `ForeachList`,
`ForeachMap`, `StringInterp`, `Parse`, `Compile`, `Call`, `MethodCall`,
`FieldAccess`, `DirectCall`.

## JIT axis

The baseline JIT (amd64) is on by default. Compare it against the interpreter
with the `BUZZ_JIT` environment variable:

```sh
BUZZ_JIT=0 go test -run='^$' -bench=LoopSum -count=8 . > interp.txt
BUZZ_JIT=1 go test -run='^$' -bench=LoopSum -count=8 . > jit.txt
benchstat interp.txt jit.txt
```

The JIT only engages on eligible top-level numeric loops (`LoopSum`,
`LoopSumFloat`, `LoopEq`, …); call-heavy or object-heavy benchmarks fall back to
the interpreter, so `BUZZ_JIT` leaves them unchanged.

## Value representation axis

The VM's `Value` has three build-tag-selected layouts. Benchmark them:

```sh
go test -run='^$' -bench=. .                  # default: 8-byte NaN-box
go test -run='^$' -bench=. -tags buzz_safe .  # safe: typed interface payload
go test -run='^$' -bench=. -tags buzz_unsafe . # unsafe: pointer struct
```

(The JIT is compiled only with the default rep; the safe/unsafe builds run on the
interpreter.)

## Cross-language comparison

`comparison/` is a separate module benchmarking gopherbuzz against gopher-lua,
tengo, and goja. Every engine runs under the same two protocols - `Warm` (reused VM)
and `Fresh` (new VM per iteration) - so the field is level; sub-benchmarks are
named `Workload/Protocol/Engine`. See `comparison/README.md`.

```sh
cd comparison && GOWORK=off go test -run='^$' -bench=. -benchmem .
cd comparison && GOWORK=off go test -run='^$' -bench='LoopSum/Warm' -benchmem . # one cell
```

## Read

Compare two runs with [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat):

```sh
go test -run='^$' -bench=. -benchmem -count=10 . > old.txt
# ...make a change...
go test -run='^$' -bench=. -benchmem -count=10 . > new.txt
benchstat old.txt new.txt
```

`-count=10` gives benchstat enough samples to report a confidence interval;
treat a delta as real only when it does so.
