# magus benchmarks

Head-to-head comparison: magus vs turbo, nx, lage, moon, bazel, and make.

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

## Caveats

- **S4-S7 measure the compiler, not magus.** Cold/incremental builds are dominated by `go build`/`tsc`; magus overhead shows cleanest in S1-S3 and the in-process micro-benchmarks.
- **Same graph, same edges.** Fixture generators emit one `magusfile.buzz` per project mirroring the edges turbo/nx derive from `package.json`/`go.work` exactly, so affected sets are comparable.
- **`ts` fixture S4-S7 are broken.** `pnpm install` doesn't reliably symlink `@bench/lib-*`, so `tsc -b` fails for all tools. Only S1-S3 are trustworthy on `ts`; `bench.sh` marks S4-S7 as `n/a`.
- **What's reliable:** the `go` fixture (magus vs make) runs end-to-end and is what's in CI. `ts`/`polyglot` and `large-monorepo` need JS toolchains and a dedicated host.
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
| `MAGUS_BIN`                | `magus` | Path to magus binary                    |
