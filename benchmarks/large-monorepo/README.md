# large-monorepo benchmark (magus vs turbo / nx / lage)

A realistic head-to-head built on [vsavkin/large-monorepo](https://github.com/vsavkin/large-monorepo)
— 5 Next.js apps + ~26k components — instead of the synthetic `fixtures/ts`
tree. This removes the fixture-maintenance burden and the `TS2307` linking
failures that made `fixtures/ts` S4–S7 unrunnable (see `../README.md`).

Status: **Phases 1–2 complete** (turbo/nx/lage baseline + magus). moon (phase 3)
and bazel (phase 4) are not wired yet.

## Layout

```
large-monorepo/
  setup.sh        clone upstream @ pinned SHA into gen/repo, write magus config, npm install
  bench.sh        run the scenarios, emit results/, write BENCHMARKS-large-monorepo.md
  spells/nextjs.tl  workspace-local Next.js spell (build = next build), copied into gen/repo
  versions.lock   pinned upstream SHA + tool versions
  gen/repo/       the checkout + generated magusfile.tl + node_modules (gitignored)
  results/        hyperfine JSON (gitignored, created by bench.sh)
```

Everything generated lives under `gen/`. That name is on magus's discovery
ignore-list (same as the synthetic fixtures' `gen/` dirs), so the cloned repo's
own `magusfile.tl` is **not** picked up by the surrounding magus workspace when
you run magus from the tack repo root.

## Why no fork and no patch

We never fork upstream and we patch nothing in its tree. magus support is purely
additive — a generated `magusfile.tl` plus the `nextjs` spell — and turbo, nx,
and lage build the clean checkout as-is (their configs already live at the repo
root). `setup.sh` checks out a pinned SHA and lays the magus config on top. When
upstream moves, bump `upstream_sha` in `versions.lock`.

## How magus is wired

magus does not infer a JS/Next.js build graph, so the generated
`gen/repo/magusfile.tl` declares it:

1. `magus.spell.load("spells/nextjs.tl")` registers a **workspace-local** spell
   whose `build` target runs `npm run build` (i.e. `next build`) and caches
   `.next/**`. It is loaded straight from Teal source at runtime — no
   `spells-generate`, no compiled Lua, nothing added to the magus binary's
   built-in spell set.
2. Each app is registered with `spell = {name = "nextjs"}` and `depends_on`
   set to the 20 `packages/<app>/important-feature-*` libraries its
   `package.json` lists. The feature/shared packages are plain auto-detected
   `ts`/`js` projects with no build target, so `magus run build` builds only the
   5 apps and treats the packages as inputs — exactly what
   `next-transpile-modules` does.

`magus.spell.load` / `magus.spell.new` / `magus.spell.import` are host bindings
(see `../../internal/interp/bindings/spell.go`); a magus built before they exist
cannot run this benchmark.

### Verified behaviour

- cold `next build` runs and caches `.next` (227 MB for `crew`);
- a warm run is an all-hit cache **replay** (magus excludes the declared
  `.next/**` outputs from the input hash, so the build's own output never
  invalidates its key);
- editing an app page busts only that app (S6);
- editing a feature lib busts that lib **and** its app via transitive
  cache-key propagation (S7), while sibling apps stay cached.

## Prerequisites

- Node + npm (see `versions.lock`), `hyperfine` (`sudo apt install hyperfine`).
- A magus binary with the `magus.spell.*` bindings. Build the dual-engine cgo
  binary for the `magus-luajit` / `magus-gopherlua` split:

  ```sh
  sudo apt install -y libluajit-5.1-dev
  CGO_ENABLED=1 go build -tags mcp,selfmanage -o ~/.local/bin/magus ./magus/cmd/magus
  ```

  turbo/nx/lage are installed locally by `setup.sh` (`npm install`); no global
  installs needed.

## Run

```sh
./setup.sh                       # clone @ pinned SHA + overlay + npm install
MAGUS_BIN=~/.local/bin/magus ./bench.sh           # all tools, all scenarios
MAGUS_BIN=~/.local/bin/magus ./bench.sh turbo nx  # a subset of tools
BENCH_DRY_RUN=1 ./bench.sh                         # print commands only
```

Results land in `results/*.json` and are aggregated into
`BENCHMARKS-large-monorepo.md` by the shared `../aggregate` tool (kept separate
from the synthetic-fixture `BENCHMARKS.md` so the two are not confused).

### Cost / tuning

A single cold app build is ~2 min, so a full multi-run sweep across four tools
is **hours** — run it on a dedicated host, not in CI. Defaults are modest:

| Env                 | Default       | Meaning                                 |
| ------------------- | ------------- | --------------------------------------- |
| `BENCH_CONCURRENCY` | `10`          | per-tool parallelism (matches upstream) |
| `BENCH_RUNS`        | `3`           | measured runs for S4/S5                 |
| `BENCH_INCR_RUNS`   | `1`           | measured runs for S6/S7                 |
| `BENCH_SCENARIOS`   | `S4 S5 S6 S7` | scenarios to run                        |
| `MAGUS_BIN`         | `magus`       | magus binary path                       |

## Scenarios

| ID  | Scenario         | What happens                                                           |
| --- | ---------------- | ---------------------------------------------------------------------- |
| S4  | Cold build       | clear each tool's cache **and** all `.next`, build everything          |
| S5  | Warm replay      | build again with a fully-populated cache                               |
| S6  | One leaf changed | edit `apps/crew/pages/index.tsx`, rebuild (only `crew` is affected)    |
| S7  | One lib changed  | edit `packages/crew/important-feature-0`, rebuild (`crew` is affected) |

Note on S7: this repo's only truly shared code is `packages/shared/*`, but those
are not declared as dependencies in any `package.json`, so **no** tool (turbo,
nx, lage, or magus) sees an edge to them. To keep the comparison fair, S7 edits
an _app-specific_ feature lib that **is** in the package graph; it has exactly
one downstream app. This differs from the original plan's "shared lib (all apps
downstream)" wording.

S1–S3 (startup / discovery / affected planning) from the synthetic harness are
not run here; this benchmark targets the build scenarios that the realistic repo
makes honest.

## What is and isn't measured

Same stance as the synthetic harness: single host, no network, local cache only,
real `next build` invocations. No remote cache / distributed execution, no
watch-mode, no CI sharding. Report `min`; `stddev` flags noise.
