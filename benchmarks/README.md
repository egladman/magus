# magus benchmarks

Measured results hold magus against make on one fixture today. turbo, nx,
lage, moon, and bazel are pinned for the future matrix but have no results
yet.

Results are in [`BENCHMARKS.md`](./BENCHMARKS.md), stamped with date,
hardware, and exact tool versions.

---

## Quick start

### Prerequisites

System packages (Debian/Ubuntu) for building `magus` and for fixture
filesystem watches that the bench scenarios may exercise:

```sh
sudo apt install -y build-essential pkg-config hyperfine inotify-tools
```

| Tool        | Install                                                   |
| ----------- | --------------------------------------------------------- |
| `hyperfine` | `sudo apt install hyperfine` (included above)             |
| `magus`     | `cd magus && go build -o ~/.local/bin/magus ./cmd/magus`  |
| `make`      | usually pre-installed                                     |
| `turbo`     | `pnpm install -g turbo@latest`                            |
| `nx`        | `pnpm install -g nx@latest`                               |
| `lage`      | `pnpm install -g @microsoft/lage@latest`                  |
| `moon`      | `curl -fsSL https://moonrepo.dev/install/moon.sh \| bash` |
| `bazel`     | see [bazel.build/install](https://bazel.build/install)    |

> `inotify-tools` is optional but useful if you hit
> `fs.inotify.max_user_watches` errors on large fixtures. Bump it with
> `sudo sysctl fs.inotify.max_user_watches=524288`.

### Run a benchmark

```sh
# Go fixture (magus vs make), 50 projects
./bench.sh go 50

# TypeScript fixture (all tools), 100 packages
./bench.sh ts 100

# TypeScript, specific tools only
./bench.sh ts 25 magus turbo nx

# Polyglot fixture
./bench.sh polyglot

# Dry run: prints hyperfine commands without executing them
BENCH_DRY_RUN=1 ./bench.sh go 8
```

magus appears as a single `magus` tool (measured daemon-off and daemon-on);
Buzz is the only magusfile language, so there is no Lua-engine axis to vary.

`bench.sh` writes results to `results/` (gitignored) and regenerates
`BENCHMARKS.md` in-place.

---

## Scenarios

| ID | Scenario                 | What is measured                                              |
| -- | ------------------------ | ------------------------------------------------------------- |
| S1 | Startup overhead         | `--version` invocation latency                                |
| S2 | Project discovery        | Time to enumerate all projects                                |
| S3 | Affected dry-run         | Planning-only after 1 file change (no compiler)               |
| S4 | Cold build               | First build with empty cache, max parallelism                 |
| S5 | Warm cache replay        | Second build; all outputs already cached                      |
| S6 | One leaf file changed    | Rebuild after touching 1 file with no downstream consumers    |
| S7 | One upstream lib changed | Rebuild after touching a shared lib (all apps are downstream) |

S7 is `n/a` on the `go` fixture: it generates N independent services with no
shared libs, so there is no upstream to change. It previously touched the same
file S6 touches and published the duplicate under the S7 heading.

`nx affected` has no `--dry-run` (that flag belongs to nx's generators, and nx
forwards an unrecognized option to the executor), so S3 uses
`nx affected --target=build --base=HEAD~1 --head=HEAD --graph=stdout`, which is
nx's planning output. nx also gets the same `--base` the other tools get:
without it nx compares against its own default base and does more or less work
than everything else in the one scenario magus is built to win.

Tools with a persistent-daemon option (magus, nx) are measured twice:
daemon off (`Daemon: off`) and daemon on (`Daemon: on`). Other tools are
measured once (`Daemon: off`, since they have no daemon mode).

---

## What is and is not measured

**Measured:**

- Wall-clock time on a single host, no network
- Local cache only (no remote caching / Nx Cloud / Turborepo Remote)
- Real compiler invocations (tsc, go build) for cold/incremental builds

**Not measured:**

- Remote cache or distributed execution (Nx Cloud, Turborepo Remote,
  Bazel Remote Execution)
- Watch-mode latency
- Cross-OS performance
- CI parallelism / sharding

---

## Caveats

- **S4-S7 measure the compiler, not magus.** Cold/incremental builds are dominated by `go build`/`tsc`; magus overhead shows cleanest in S1-S3 and the in-process micro-benchmarks.
- **Same graph, same edges.** Fixture generators emit one `magusfile.buzz` per project mirroring the edges turbo/nx derive from `package.json`/`go.work` exactly, so affected sets are comparable.
- **`ts` fixture S4-S7 are broken.** `pnpm install` doesn't reliably symlink `@bench/lib-*`, so `tsc -b` fails for all tools. Only S1-S3 are trustworthy on `ts`; `bench.sh` marks S4-S7 as `n/a`.
- **What's reliable:** the `go` fixture (magus vs make) runs end-to-end. `ts`/`polyglot` and `large-monorepo` need JS toolchains and a dedicated host.
- **Nothing here runs in CI, so a fixture can rot without anything failing.** No workflow references `bench.sh` or the fixtures, and there is no `bench` target. That is not a gap to fill (these benchmarks want a quiet dedicated host, which CI is not) but it does mean the generated magusfiles are only exercised when a person runs them. They had gone stale across three separate changes at once - the removed `magus.project.register`/`magus.needs` API, the `ts` -> `typescript` and `py` -> `python` spell renames, and a cross-project import written workspace-relative when magus resolves it dot-relative - so every generated workspace failed to load until 2026-08. Load one before trusting a run: `./fixtures/go/gen.sh 3 && magus --root fixtures/go/gen ls`.
- **A failed run is not a fast run.** `bench.sh` passes `--ignore-failure`, so a tool that crashes still produces a timing. The aggregator reads hyperfine's `exit_codes` and prints `FAILED` in place of every timing column, with the exit code below the table; failed rows never sort to the top and never reach the chart.
- **Published tool versions are probed, not copied.** `BENCHMARKS.md` lists the versions of the tools that produced rows, read from the binaries at aggregation time. `versions.lock` states the intent; it is not evidence that a tool ran.
- **Tooling:** `bazel`/`moon` installs are environment-sensitive (may show `excluded - install failed`). `turbo`/`nx`/`lage` require pnpm global bin on `PATH`.

---

## Fixtures

| Fixture    | Size arg                 | What's generated                                         |
| ---------- | ------------------------ | -------------------------------------------------------- |
| `go`       | N                        | N independent Go services (no shared libs)               |
| `ts`       | N (≥ 10, divisible by 5) | N/5 shared TS libs + N/5 apps each depending on all libs |
| `polyglot` | none                     | 1 Go service + 2 TS packages + 1 Python tool             |

Fixtures are generated on-demand (`gen.sh N`) into `fixtures/<fixture>/gen/`
(gitignored). The same `N` always produces a byte-identical tree.

---

## Reproducing BENCHMARKS.md

```sh
./bench.sh go 50
./bench.sh ts 100
./bench.sh polyglot
BENCH_RUNS=20 ./bench.sh ts 100   # lower variance
```

---

## Tuning

| Env var                    | Default | Description                             |
| -------------------------- | ------- | --------------------------------------- |
| `BENCH_WARMUP`             | `1`     | Hyperfine warmup iterations             |
| `BENCH_RUNS`               | `10`    | Hyperfine measurement iterations        |
| `BENCH_SKIP_VERSION_CHECK` | `0`     | Skip `versions.lock` version comparison |
| `BENCH_DRY_RUN`            | `0`     | Print commands without running          |
| `BENCH_JOBS`               | `8`     | Parallelism handed to every tool        |
| `MAGUS_BIN`                | `magus` | Path to magus binary                    |
| `MAKE_BIN`                 | `make`  | Path to GNU Make (use `gmake` on macOS) |

`BENCH_DRY_RUN=1` prints the hyperfine command lines and does nothing else: no
cache clears, no warm-up builds, no scratch commits, no daemon restarts, and
`BENCHMARKS.md` is left untouched.

### Parallelism

Every tool is given `BENCH_JOBS` explicitly (`--concurrency`, `-j`,
`--parallel`, `--jobs` depending on the tool). Left to their own defaults, the
tools without a flag took the host core count while the flagged ones took 8,
which on a 10-core machine is a ~25% wider pool for some of the field.

### Which make

`versions.lock` pins GNU Make 4.4.1. macOS ships GNU Make 3.81 as `make`, so a
mac run needs `MAKE_BIN=gmake` (Homebrew) to compare against the pinned
version. The version check warns when it does not match, and `BENCHMARKS.md`
records the version of the binary that actually ran.
