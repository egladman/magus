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

Read the ratios, not the milliseconds. One run at 15x carries real noise -- upstream's
`strinterp` moved 21% between two runs of this same suite on an idle machine -- so treat
anything inside roughly +/-25% as a tie. Allocation counts, in `../../bench_test.go`, are
the stable number when a change needs proving.

| workload        | gopherbuzz | upstream | gb net | up net |     ratio |
| --------------- | ---------: | -------: | -----: | -----: | --------: |
| `startup`       |       3.41 |     2.06 |      - |      - |      1.7x |
| `strinterp`     |      53.97 |    72.98 |  50.56 |  70.92 |  **0.7x** |
| `fiber`         |       7.59 |     6.15 |   4.18 |   4.09 |  **1.0x** |
| `mapops`        |      31.28 |    22.58 |  27.87 |  20.52 |      1.4x |
| `trycatch`      |      18.87 |     8.39 |  15.46 |   6.33 |      2.4x |
| `listops`       |      26.51 |     8.87 |  23.10 |   6.81 |      3.4x |
| `fib`           |      22.59 |     6.38 |  19.18 |   4.32 |      4.4x |
| `loopsum`       |      46.84 |    10.49 |  43.43 |   8.43 |      5.2x |
| `closure`       |      43.22 |     9.52 |  39.81 |   7.46 |      5.3x |
| `mandelbrot`    |     380.86 |    65.57 | 377.45 |  63.51 |      5.9x |
| `object`        |      41.52 |     8.12 |  38.11 |   6.06 |      6.3x |
| `foreach_range` |      40.28 |     7.75 |  36.87 |   5.69 |      6.5x |
| `match`         |     110.68 |     9.86 | 107.27 |   7.80 | **13.8x** |

**Geometric mean 3.5x, median 5.2x.** That is the honest one-line answer: upstream is
roughly three to five times faster across this set. The shape is expected - upstream
compiles to native code through MIR, gopherbuzz is a bytecode interpreter written in Go

- and a 3-5x gap against a JIT is a respectable place for an interpreter to sit. The
  interesting rows are the ones that depart from it.

**gopherbuzz WINS `strinterp`** (0.7x) and ties `fiber` (1.0x). The long-standing
"strings are gopherbuzz's soft spot" framing is now wrong: string building is the most
expensive non-`mandelbrot` workload for UPSTREAM, and gopherbuzz does it faster.

**`match` is still the outlier at 13.8x**, down from 20.4x. Interning one Value per enum
case removed 71% of `BenchmarkMatchEnum`'s allocations (175007 -> 50007): every arm
comparison used to heap-allocate, take a global mutex, and append a permanent entry to
the global heap table. What remains is measured and not yet done - `rangeValue` allocates
on every range arm even though the bounds are compile-time constants, and folding it into
the const pool needs a bytecode format bump because the codec has no `tagRange`.
Separately `strMethod` allocates a closure plus a wrapper on every string method call,
which is a larger and more general win than match.

**`mapops` at 1.4x** is the next-best relative showing, so the map implementation is not
where the remaining time is.

## Dialect notes

The programs are constrained to what BOTH engines accept, which is narrower than
either alone. These each cost real time to find:

- **`fib` is a reserved word** in both. The recursive workload is named `fibo`.
- **A script needs `fun main(_: [str]) > int`.** Upstream runs `main`, and the int
  return is the exit status, which is how each program reports its own answer.
- **Ints are 48-bit on both engines, not 64.** The literal ceiling is exactly
  2^47-1 = `140737488355327`; `140737488355328` is `[E78] int overflow` at parse
  time. The ARITHMETIC has the same range rather than a wider one - `140737488355327
  - 1`evaluates to`-140737488355328`on upstream and on gopherbuzz alike, so the
  literal limit is the value limit. Expected values are sized under it, which is why`fiber` sums 50 000 squares rather than 200 000. gopherbuzz reproducing the wrap
    exactly is conformance, not a shared bug to route around.
- **No int/double coercion.** `px + 0.0` where `px` is an `int` is a type error, not
  a promotion, so `mandelbrot` uses double grid counters throughout.
- **A non-optional fiber yield type is now legal** (upstream `fb54e7b`). `fiber`
  declares `*> Tracked` and `*> int`; under the older rule `foreach` bound the loop
  variable as an optional and the arithmetic needed an unwrap.
- **The stdlib import prefix is `buzz:`** (`import "buzz:std"`), and it binds a
  NAMESPACE. A bare `import "std"` resolves against the SCRIPT's directory upstream
  and fails; after `import "buzz:std"`, bare `print("hi")` is `[E75] print is not
  defined` and `std\print("hi")` is the working form. No program here needs it --
  they avoid printing so that neither engine's I/O is on the clock -- but a new one
  would hit both halves.

## Adding a workload

Write it into `programs/`, make it self-checking, and run `TestProgramsAgree`
before timing it. A program that only one engine accepts does not belong here; put
it in `../../bench_test.go` instead, which measures gopherbuzz alone and in-process.
