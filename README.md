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

### The web and daemon stack

magus serves three browser surfaces - the dashboard, the graph explorer, and the log viewer - plus the MCP endpoint for agents. They share one layering, arrows pointing down:

```text
transport     internal/httpx        one loopback *http.Server + composable middleware
                                     (CORS, RequireLoopbackPeer, GuardRebind, BearerGuard, Gzip)
                                     + the generic one-shot BlobServer
auth          internal/auth         the daemon's bearer-token store (guards /mcp AND /api)
handler       internal/handler/*    domain<->proto WIRE mapping + HTTP route handlers as
                                     receiver-method http.Handler types; mirrors the proto packages
service       internal/service/*    PURE application logic - no net/http, no proto
repository    internal/cache, knowledge   data access (cache.OutputStore, the knowledge graph)
composition   internal/daemon       assembles the daemon HTTP server from all of the above
```

A `handler` subpackage is named for and owns the wire concerns of the proto package `magus.<name>.v1` (`handler/viewer`<->`viewer.v1`, `handler/status`<->`status.v1`, `handler/graph`<->`graph.v1`); see [`internal/handler/README.md`](https://github.com/egladman/magus/blob/main/internal/handler/README.md). Route handlers hold a narrow interface satisfied by the service; `internal/service/console` holds the web-UI logic and imports no `net/http`.

The MCP tools operate on a `*magus.Magus`, so `handler/mcp` imports the root `magus` package - which means the daemon (it imports `handler/mcp`) cannot live in `magus` without a cycle. Instead the root `Magus` holds an injected `Daemon` interface field, populated only in daemon mode by the CLI (`m.SetDaemon(daemon.New(opts))`), so the workspace owns the server without importing its assembler. The CLI's ephemeral `graph open --serve` and `run --live` servers reuse the same `httpx` transport, which is why it is a foundational package rather than part of `daemon`.

Two house rules: no `_test`-suffixed test packages (a test file uses the same package as the code it tests), and MCP is always compiled in (no build tags).

[^docs-source]: Source: [website/](https://github.com/egladman/magus/tree/main/website).
