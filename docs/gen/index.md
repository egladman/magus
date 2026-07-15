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
- **Reference** - [Man pages](docs/manpage/gen/magus.md), [Standard library modules](docs/buzz/modules/index.md), [Debugging](docs/debugging.md), [Output references](docs/output-refs.md), [Tips and tricks](docs/tips.md)

## Install

magus ships as a single self-contained binary. See the [Download guide](docs/download.md).

## Optional browser UI

magus is fully featured from the terminal - everything here is optional. Alongside the CLI, it can drive three read-only browser surfaces:

> **See it first:** [open the live demo](https://eli.gladman.cc/magus/console/dashboard/#demo) - no install, no daemon. It fills the dashboard with synthesized activity, streams a build into the log viewer, and lets you jump between all three apps in demo mode. Everything below runs against your own daemon instead.

- **[Dashboard](https://eli.gladman.cc/magus/console/dashboard/)** - live daemon health, the concurrency pool, running targets, and cache activity.
- **[Graph explorer](https://eli.gladman.cc/magus/console/graph/)** - navigate targets, spells, and their dependency graph (`magus graph open`).
- **[Log viewer](https://eli.gladman.cc/magus/console/logs/)** - read or stream any past run's captured output (`magus query output <ref> --open`).

These are complementary add-ons, not a runtime you depend on. Two things set them apart architecturally:

- **The binary serves no HTML.** magus never embeds a web server that ships a UI. The pages are a separate static site (built under [`website/gen/`](https://github.com/egladman/magus/tree/main/website/gen), hosted at [eli.gladman.cc/magus](https://eli.gladman.cc/magus/), or self-hosted from any file server). All the daemon exposes is a small read-only API over loopback (`/api/v1/...`) plus the MCP endpoint - no page serving, no write routes.
- **Your data never leaves your machine.** The hosted page talks only to `127.0.0.1`/`[::1]` - a loopback lock the page enforces before any request - or receives your graph inline through a URL fragment. Nothing is uploaded. You can drop the UI entirely: the daemon runs fine without it (`bridge.enabled: false`), and a binary built without `-tags mcp` has no browser API at all. See the [Console reference](https://eli.gladman.cc/magus/console/).

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
│   ├── ward/             coded op diagnostics (the MGSxxxx wards)
│   ├── workspace/        project + magusfile discovery
│   ├── observability/    OpenTelemetry traces and metrics
│   ├── httpx/            loopback HTTP transport + composable middleware/guards
│   ├── handler/          domain<->proto wire mapping + HTTP route handlers (mirrors proto);
│   │                     includes handler/mcp (the Model Context Protocol server)
│   ├── service/          application logic: shared-service ops, and service/console (web UI)
│   ├── daemon/           assembles the daemon HTTP server (MCP + /api) from the above
│   ├── auth/             the daemon's bearer-token store
│   └── ...               (cache, proc, retry, selfupdate, render, report, doctor, describe, ...)
├── gopherbuzz/           the embedded Buzz interpreter
├── std/                  magusfile stdlib (fs, os, http, json, crypto, ...)
├── spells/               built-in language spells (go, rust, typescript, ...)
├── schema/               generated magus.yaml / MAGUS_* config inventory
├── docs/                 documentation source (rendered by `magus run generate website`)
└── website/              docs-site generator (Buzz-based static site)
```

[^docs-source]: Source: [website/](https://github.com/egladman/magus/tree/main/website).
