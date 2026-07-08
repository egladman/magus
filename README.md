# magus

<p align="center">
  <picture>
    <source srcset="./assets/gopher.webp" type="image/webp">
    <img alt="magus gopher mascot" width="400" height="267" fetchpriority="high" src="./assets/gopher.png">
  </picture>
</p>

<!-- Coverage is generated locally by `magus run coverage` (Go toolchain only, no third-party service); regenerate and commit to refresh. -->

<a href="https://github.com/egladman/magus/actions/workflows/ci.yaml"><img alt="CI" src="https://github.com/egladman/magus/actions/workflows/ci.yaml/badge.svg"></a> <img alt="Coverage" width="113" height="20" src="./assets/coverage.svg"> <a href="https://pkg.go.dev/github.com/egladman/magus"><img alt="Go Reference" src="https://pkg.go.dev/badge/github.com/egladman/magus.svg"></a>

A fast cross-platform task orchestrator for polyglot (mono)repos.

Magus computes affected projects from your changes, caches the results, and runs the minimum rebuild set after a change.

It is a single statically typed binary that takes its configuration as code, with no second toolchain to install.

---

## Documentation

Full docs live at **[eli.gladman.cc/magus](https://eli.gladman.cc/magus/)**.[^docs-source] The major sections:

- **Core concepts** - [Targets](docs/targets.md), [Spells](docs/spells.md), [Charms](docs/charms.md), [Operations](docs/operations.md), [Services](docs/services.md)
- **Running at scale** - [CI](docs/ci.md), [Daemon](docs/daemon.md), [Remote caching](docs/remote-cache.md), [MCP](docs/mcp.md), [Telemetry](docs/telemetry.md)
- **Reference** - [Man pages](docs/manpage/gen/magus.md), [Standard library modules](docs/buzz/modules/index.md), [Debugging](docs/debugging.md), [Tips and tricks](docs/tips.md)

## Install

magus ships as a single self-contained binary. See the [Download guide](docs/download.md).

---

## Development

For the full contributor reference - the [Contributing guide](https://eli.gladman.cc/magus/development/contributing/), per-project target catalogs (run order + dependency graphs), and the config reference - see the [Development page](https://eli.gladman.cc/magus/development/).

Building magus needs Go. The full toolchain - Go itself, plus Node and esbuild for the docs site and TinyGo for the WebAssembly playground - is pinned in [`mise.toml`](https://github.com/egladman/magus/blob/main/mise.toml); [mise](https://mise.jdx.dev/) installs it in one step. From a fresh clone:

```sh
git clone https://github.com/egladman/magus
cd magus
mise install           # installs the pinned Go, Node, esbuild, and TinyGo
go build -o magus ./cmd/magus
```

Only building the `magus` binary? Go alone is enough and you can skip `mise install`; you need it for the docs site (`magus run generate website`) and the playground.

Run the tests through magus itself - the whole point is that magus builds and tests magus:

```sh
magus run ci
```

### Project layout

```text
magus/
├── cmd/
│   ├── magus/            CLI entry point
│   ├── magus-utils/      release signing + config-doc generators
│   ├── magus-docs/       stdlib module doc generator
│   └── magus-manpage/    man-page generator
├── internal/             core engine (~30 packages), the notable ones:
│   ├── depgraph/         affected-set computation over the project graph
│   ├── cache/            content-addressed build cache
│   ├── run/              target scheduling and the run hierarchy
│   ├── sandbox/          filesystem + exec isolation for target runs
│   ├── service/          long-running shared service ops (+ serviceident, serviceaudit)
│   ├── ward/             coded op diagnostics (the MGSxxxx wards)
│   ├── mcp/              Model Context Protocol server
│   ├── workspace/        project + magusfile discovery
│   ├── observability/    OpenTelemetry traces and metrics
│   └── ...               (proc, retry, selfupdate, render, report, doctor, describe, ...)
├── gopherbuzz/           the embedded Buzz interpreter
├── std/                  magusfile stdlib (fs, os, http, json, crypto, ...)
├── spells/               built-in language spells (go, rust, typescript, ...)
├── schema/               generated magus.yaml / MAGUS_* config inventory
├── docs/                 documentation source (rendered by `magus run generate website`)
└── website/              docs-site generator (Buzz-based static site)
```

[^docs-source]: Source: [website/](https://github.com/egladman/magus/tree/main/website).
