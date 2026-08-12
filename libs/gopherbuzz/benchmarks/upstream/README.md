# gopherbuzz vs upstream Buzz

Runs identical `.buzz` source through gopherbuzz and through the reference Zig
implementation, as processes, and compares wall clock.

## Why processes, and what that costs

`benchmarks/comparison/` compares embedded VMs inside one Go process, where a warm
VM is reused across iterations. That protocol is unavailable here: upstream Buzz is
a separate binary. The only measurement both engines can offer is whole-process
wall clock -- fork, load, compile, run, exit -- so that is what both are measured
on, gopherbuzz included. Anything else would compare a warm loop against a cold
process.

So every number carries startup and compilation. The `startup` row is a program
whose body is `return 0;`, reporting that floor; the "net" columns below subtract
it. Allocations are not reported at all: Go's `-benchmem` sees only the harness
process's heap, and neither engine runs in it.

Each program checks its own answer and exits non-zero on a wrong one, so a
fast-but-wrong run fails the benchmark instead of posting a good time.
`TestProgramsAgree` runs that check for both engines without timing anything, and
is the gate to fix first when this suite goes red.

## Running it

```sh
magus run buzz-build libs/gopherbuzz
go test ./benchmarks/upstream/ -bench . -benchtime 15x -run XXX
```

Both binaries are located by convention and the suite SKIPS rather than fails when
either is missing: gopherbuzz at `../../buzz` (`GOPHERBUZZ_BIN` overrides), upstream
at `~/Repos/buzz/zig-out/bin/buzz` (`GOPHERBUZZ_UPSTREAM_DIR` overrides, following
`conformance_test.go`).

## Results

Apple M5, darwin/arm64, `-benchtime 15x`, upstream at `0.5.0-265-g294d8f9` built
`-Doptimize=ReleaseSafe`. Milliseconds per run; "net" subtracts the startup floor.
Ratio is upstream-relative, so >1 means upstream is faster.

| workload        | gopherbuzz | upstream | gb net | up net |    ratio |
| --------------- | ---------: | -------: | -----: | -----: | -------: |
| `startup`       |       3.54 |     2.13 |      - |      - |     1.7x |
| `fiber`         |       6.68 |     6.23 |   3.14 |   4.10 | **0.8x** |
| `strinterp`     |      60.36 |    60.11 |  56.82 |  57.98 | **1.0x** |
| `mapops`        |      33.21 |    23.51 |  29.68 |  21.39 |     1.4x |
| `trycatch`      |      19.93 |     9.57 |  16.39 |   7.44 |     2.2x |
| `listops`       |      26.81 |     8.96 |  23.27 |   6.83 |     3.4x |
| `fib`           |      23.35 |     6.42 |  19.81 |   4.30 |     4.6x |
| `loopsum`       |      46.34 |    10.57 |  42.80 |   8.44 |     5.1x |
| `closure`       |      45.25 |    10.11 |  41.72 |   7.99 |     5.2x |
| `object`        |      42.39 |     8.84 |  38.85 |   6.71 |     5.8x |
| `mandelbrot`    |     395.38 |    67.56 | 391.84 |  65.43 |     6.0x |
| `foreach_range` |      41.83 |     7.72 |  38.29 |   5.59 |     6.8x |
| `match`         |     164.89 |    10.03 | 161.35 |   7.90 |  **20x** |

Upstream leads nearly everywhere, which is the expected shape: it compiles to
native code through MIR, and gopherbuzz is a bytecode interpreter written in Go.
The interesting rows are the ones that depart from that shape.

**`match` is the outlier and the actionable one.** 20x is not the ~5x the
interpreter/JIT gap explains elsewhere, so it is a gopherbuzz problem rather than a
structural one. `BenchmarkMatchEnum` in `../../bench_test.go` shows why: roughly 7
allocations per evaluated arm. Reducing that is the single largest win available
here, and it is worth taking before any of the broad interpreter work.

**`fiber` is gopherbuzz's win**, and the only one. A fiber switch costs less here
than upstream's, which is worth keeping an eye on as a property rather than an
accident.

**`strinterp` is a dead heat**, and both are slow in absolute terms -- it is the
most expensive non-`mandelbrot` workload for upstream too. So gopherbuzz's
long-standing "strings are the soft spot" framing is only half right: the gap to
upstream has closed here, and what remains is that string building is expensive in
both.

**`mapops` at 1.4x** is the second-best relative showing, so the map implementation
is not where the remaining time is.

## Dialect notes

The programs are constrained to what BOTH engines accept, which is narrower than
either alone. These each cost real time to find:

- **`fib` is a reserved word** in both. The recursive workload is named `fibo`.
- **A script needs `fun main(_: [str]) > int`.** Upstream runs `main`, and the int
  return is the exit status, which is how each program reports its own answer.
- **Upstream's int LITERAL ceiling is ~4e13**, well below the 64-bit range the
  arithmetic itself handles: `41665416675000` parses, `333328333350000` is a
  `[E78] int overflow` at parse time. Expected values are sized under it, which is
  why `fiber` sums 50 000 squares rather than 200 000.
- **No int/double coercion.** `px + 0.0` where `px` is an `int` is a type error, not
  a promotion, so `mandelbrot` uses double grid counters throughout.
- **A non-optional fiber yield type is now legal** (upstream `fb54e7b`). `fiber`
  declares `*> Tracked` and `*> int`; under the older rule `foreach` bound the loop
  variable as an optional and the arithmetic needed an unwrap.
- **The stdlib import prefix is `buzz:`** (`import "buzz:std"`). A bare
  `import "std"` resolves against the SCRIPT's directory upstream and fails. No
  program here needs it -- they avoid printing so that neither engine's I/O is on
  the clock -- but a new one would.

## Adding a workload

Write it into `programs/`, make it self-checking, and run `TestProgramsAgree`
before timing it. A program that only one engine accepts does not belong here; put
it in `../../bench_test.go` instead, which measures gopherbuzz alone and in-process.
