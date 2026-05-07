# buzz benchmarks

Microbenchmarks for the Buzz interpreter. The benchmark code lives in
`buzz/bench_test.go` (and `buzz/vm/*_test.go`); this directory holds only the
optimization analysis. Result dumps are not committed — regenerate them.

## Run

```sh
# all buzz benchmarks
go test -bench=. -benchmem ./buzz/...

# one benchmark
go test -bench=BenchmarkLoopSum -benchmem ./buzz
```

Benchmarks: `Fib`, `LoopSum`, `LoopEq`, `ForeachList`, `ForeachMap`,
`StringInterp`, `Parse`, `Compile`, `Call`, `MethodCall`, `FieldAccess`,
`DirectCall`.

## Value representation axis

The VM's `Value` has two build-tag-selected layouts. Benchmark both:

```sh
go test -bench=. ./buzz/...                 # default: unsafe.Pointer payload
go test -bench=. -tags buzz_safe ./buzz/... # safe: typed interface payload
```

## Read

Compare two runs with [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat):

```sh
go test -bench=. -benchmem -count=10 ./buzz > old.txt
# ...make a change...
go test -bench=. -benchmem -count=10 ./buzz > new.txt
benchstat old.txt new.txt
```

`-count=10` gives benchstat enough samples to report a confidence interval;
treat a delta as real only when it does so.
