# magus benchmarks

Head-to-head comparison: magus vs turbo, nx, lage, moon, bazel, and make.

Results are in [`BENCHMARKS.md`](./BENCHMARKS.md) — stamped with date,
hardware, and exact tool versions.

---

## Quick start

### Prerequisites

System packages (Debian/Ubuntu) — for building `magus` and
for fixture filesystem watches that the bench scenarios may exercise:

```sh
sudo apt install -y build-essential pkg-config hyperfine inotify-tools
```

| Tool        | Install                                                                  |
| ----------- | ------------------------------------------------------------------------ |
| `hyperfine` | `sudo apt install hyperfine` (included above)                            |
| `magus`     | `cd magus && go build -o ~/.local/bin/magus ./cmd/magus`            |
| `make`      | usually pre-installed                                                    |
| `turbo`     | `pnpm install -g turbo@latest`                                                                                   |
| `nx`        | `pnpm install -g nx@latest`                                                                                      |
| `lage`      | `pnpm install -g @microsoft/lage@latest`                                                                         |
| `moon`      | `curl -fsSL https://moonrepo.dev/install/moon.sh \| bash`                                                        |
| `bazel`     | see [bazel.build/install](https://bazel.build/install)                                                           |

> `inotify-tools` is optional but useful if you hit
> `fs.inotify.max_user_watches` errors on large fixtures — bump it with
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

# Dry run — prints hyperfine commands without executing them
BENCH_DRY_RUN=1 ./bench.sh go 8
```

magus appears as a single `magus` tool (measured daemon-off and daemon-on);
Buzz is the only magusfile language, so there is no Lua-engine axis to vary.

Results are written to `results/` (gitignored) and `BENCHMARKS.md` is
regenerated in-place.

---

## Scenarios

| ID  | Scenario                 | What is measured                                              |
| --- | ------------------------ | ------------------------------------------------------------- |
| S1  | Startup overhead         | `--version` invocation latency                                |
| S2  | Project discovery        | Time to enumerate all projects                                |
| S3  | Affected dry-run         | Planning-only after 1 file change (no compiler)               |
| S4  | Cold build               | First build with empty cache, max parallelism                 |
| S5  | Warm cache replay        | Second build; all outputs already cached                      |
| S6  | One leaf file changed    | Rebuild after touching 1 file with no downstream consumers    |
| S7  | One upstream lib changed | Rebuild after touching a shared lib (all apps are downstream) |

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

## Current limitations & known issues

This harness works, but several caveats matter when reading the numbers.
They're documented here so the next person doesn't rediscover them — and they
motivate the planned move off the procedural fixtures.

### S4-S7 measure the compiler, not magus

Cold/incremental builds are dominated by the real compiler (`go build`,
`tsc`), so S4-S7 mostly measure the toolchain, with magus's planning/cache
overhead layered on top. magus's own overhead shows up cleanest in **S1-S3**
(startup, discovery, affected planning) and the in-process micro-benchmarks
(`internal/...` `Benchmark*` and the `cmd/magus` `BenchmarkStartup*` set).

### Apples-to-apples: same graph, same edges

magus needs an explicit magusfile per project; it does not infer a build graph
from `package.json`/`go.work` the way turbo/nx derive theirs. The fixture
generators emit one `magusfile.buzz` per project whose edges mirror exactly what
the other tools traverse: a project-level `depends_on` (drives the **affected**
set, S3) plus a target-level `magus.needs(lib.build)` handle (drives build
**ordering** and **cache-key propagation**, S5-S7). Same nodes, same edges, so
S3's affected set and the incremental blast radius are comparable, not magus
seeing a coarser (or finer) graph than its peers.

### The `ts` fixture is not end-to-end runnable (S4–S7)

The synthetic TypeScript fixture wires cross-package deps via `workspace:*`,
but `pnpm install` in the generated tree does not reliably symlink
`@bench/lib-*` into each app's `node_modules`, so `tsc -b` fails with
`TS2307: Cannot find module '@bench/lib-N'`. This blocks S4–S7 for **all**
tools on the `ts` fixture, not just magus.

Build _ordering_ is not the blocker: magus honours the magusfile's
`magus.needs` edges, so every lib finishes before its app starts. The remaining
blocker is the fixture's package linking. Consequently:

- Only **S1–S3** are trustworthy on the `ts` fixture today.
- `bench.sh` marks magus S4–S7 on `ts` as `n/a` and runs hyperfine with
  `--ignore-failure` so partial build failures don't abort the whole run.

### What is reliable

- The **`go` fixture** runs end-to-end (`magus` vs `make`); the committed
  `BENCHMARKS.md` is this fixture, run in the CI sandbox.
- **S1-S3** across all fixtures.
- The multi-tool `ts`/`polyglot` rows and the `large-monorepo/` suite need the
  JS toolchains installed and a dedicated host; they are not run in CI.

### Tooling brittleness

- `bazel` (via bazelisk) and `moon` installs are environment-sensitive; a row
  may show `excluded — install failed` rather than fail the run.
- `turbo` / `nx` / `lage` require the pnpm global bin dir on `PATH`.

### Methodology

Single host, no network; report `min`, flag `stddev`. No remote cache,
distributed execution, watch-mode latency, cross-OS, or CI sharding.

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

The results in `BENCHMARKS.md` were generated on the hardware listed in
that file's Environment section. To reproduce on your own machine:

```sh
./bench.sh go 50
./bench.sh ts 100
./bench.sh polyglot
```

Then check `BENCHMARKS.md`. Numbers will differ based on your CPU and
available cores. Use `BENCH_RUNS=20` for lower variance:

```sh
BENCH_RUNS=20 ./bench.sh ts 100
```

---

## Tuning

| Env var                    | Default | Description                             |
| -------------------------- | ------- | --------------------------------------- |
| `BENCH_WARMUP`             | `1`     | Hyperfine warmup iterations             |
| `BENCH_RUNS`               | `10`    | Hyperfine measurement iterations        |
| `BENCH_SKIP_VERSION_CHECK` | `0`     | Skip `versions.lock` version comparison |
| `BENCH_DRY_RUN`            | `0`     | Print commands without running          |
| `MAGUS_BIN`                | `magus` | Path to magus binary                    |
