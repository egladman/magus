---
title: magus
description: A fast cross-platform task orchestrator for polyglot monorepos. Single binary, code as configuration, statically typed, no second toolchain.
tags: [magus, task orchestrator, build system, monorepo, polyglot, buzz, affected, cache]
---

# magus

<p align="center">
  <picture>
    <source srcset="./assets/gopher.webp" type="image/webp">
    <img alt="magus gopher mascot" width="400" height="300" fetchpriority="high" src="./assets/gopher.png">
  </picture>
</p>

<!-- Generated locally by `magus run coverage` (Go toolchain only, no third-party service); regenerate and commit to refresh. -->
<img alt="Coverage" width="113" height="20" src="./assets/coverage.svg">

A fast cross-platform task orchestrator for polyglot (mono)repos.

Magus computes affected projects from your changes, caches the results, and runs the minimum rebuild set after a change.

Single binary. Code as configuration. Statically typed. No second toolchain.

---

## Documentation

Full docs live at **<https://eli.gladman.cc/magus/>** ([source](docs/documentation.md)). Start there for targets, spells, charms, CI, remote caching, the daemon, MCP, telemetry, and the rest.

## Install

magus ships as a single self-contained binary. See the [Download guide](docs/download.md).

---

## Development

Building magus itself. Requires Go 1.25+; [mise](https://mise.jdx.dev/) is recommended for the pinned toolchain (see [`mise.toml`](https://github.com/egladman/magus/blob/main/mise.toml)).

```sh
git clone https://github.com/egladman/magus
cd magus
go build -o magus ./cmd/magus
```

Run the tests:

```sh
go test ./...
```

Or, once magus is built, via its own targets:

```sh
magus run test
magus run ci
```

### Project layout

| Path | Purpose |
|---|---|
| [`cmd/magus/`](https://github.com/egladman/magus/tree/main/cmd/magus) | CLI entry point |
| [`cmd/magus-utils/`](https://github.com/egladman/magus/tree/main/cmd/magus-utils) | release signing helper |
| [`internal/`](https://github.com/egladman/magus/tree/main/internal) | core engine (affected set, cache, sandbox, daemon, MCP) |
| [`gopherbuzz/`](https://github.com/egladman/magus/tree/main/gopherbuzz) | the embedded Buzz interpreter |
| [`std/`](https://github.com/egladman/magus/tree/main/std) | magusfile stdlib (`fs`, `os`, `http`, ...) |
| [`spells/`](https://github.com/egladman/magus/tree/main/spells) | built-in language spells (`go`, `rust`, `typescript`, ...) |
| [`docs/`](https://github.com/egladman/magus/tree/main/docs) | documentation source (rendered by `magus run generate website`) |
| [`website/`](https://github.com/egladman/magus/tree/main/website) | docs-site generator (Buzz-based static site) |

### Contributing

See [CONTRIBUTING.md](https://github.com/egladman/magus/blob/main/CONTRIBUTING.md). Larger changes are worth discussing first in [Discussions](https://github.com/egladman/magus/discussions).

---

## License

MIT - see [LICENSE](https://github.com/egladman/magus/blob/main/LICENSE).
