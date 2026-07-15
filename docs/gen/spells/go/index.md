---
title: go spell
description: "Go toolchain spell: build, test, vet, fmt, mod-tidy, golangci-lint, and govulncheck as magus ops."
tags: [go, spell, golang, build, test, lint, tools]
---

# go

The `go` spell wires the Go toolchain into a magusfile: each op forks a `go` (or `gofmt`) subcommand directly, with no shell. Lint and vulnerability scanning run as `go tool` invocations so they resolve from the module's tool block rather than PATH.

**Runtime name:** `go` (source `spells/golang/`)

**Version probe:** `go version`

## Passing arguments to ops

Every op is invoked as `go["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `go["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L108) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L105) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L112) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L120) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## go-build

**Command:** `go build`

### Example

<!-- run-recorder -->
```buzz
// Wire go-build into a `build` target. `magus run build` forks `go build`.
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun build(args: [str]) > void {
    go["go-build"]();
}
```

## go-clean

**Command:** `go clean ./...`

### Example

<!-- run-recorder -->
```buzz
// Wire go-clean into a `clean` target: `magus run clean` forks `go clean ./...`.
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun clean(args: [str]) > void {
    go["go-clean"]();
}
```

## go-fmt

**Command:** `gofmt -l .`

### rw

Replaces `-l` with `-w`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "replace",
    "path": "/0",
    "value": "-w"
  }
]
```

</details>

### Example

<!-- run-recorder -->
```buzz
// go-fmt lists misformatted files; the rw charm rewrites them in place.
// `magus run format` checks, `magus run format:rw` applies gofmt.
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun format(args: [str]) > void {
    go["go-fmt"]();
}
```

## go-generate

**Command:** `go generate ./...`

### Example

<!-- run-recorder -->
```buzz
// Wire go-generate into a `generate` target: `magus run generate` forks
// `go generate ./...`.
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun generate(args: [str]) > void {
    go["go-generate"]();
}
```

## go-mod-tidy

tidy checks by default (--diff exits non-zero if go.mod/go.sum need changes — safe for CI gating); the write charm applies the changes.

**Command:** `go mod tidy --diff`

### rw

Drops `--diff`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "remove",
    "path": "/2"
  }
]
```

</details>

### Example

<!-- run-recorder -->
```buzz
// go-mod-tidy checks go.mod/go.sum with --diff (CI-safe); the rw charm drops
// --diff so `magus run tidy:rw` applies the changes.
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun tidy(args: [str]) > void {
    go["go-mod-tidy"]();
}
```

## go-test

The cd charm instruments the run with an atomic-mode coverage profile written to coverage.out — the deliverable a CD pipeline ships to a coverage service (e.g. Coveralls). `magus run go::go-test:cd` (or ci:cd) emits the profile.

**Command:** `go test ./...`

### cd

Appends `-covermode=atomic`, appends `-coverprofile=coverage.out`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "-covermode=atomic"
  },
  {
    "op": "add",
    "path": "/-",
    "value": "-coverprofile=coverage.out"
  }
]
```

</details>

### debug

Appends `-v`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "-v"
  }
]
```

</details>

### Example

<!-- run-recorder -->
```buzz
// go-test runs the suite; here with the race detector, so `magus run test` forks
// `go test ./... -race`. The cd charm (`magus run test:cd`) adds the atomic
// coverage profile a CD pipeline ships.
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun test(args: [str]) > void {
    go["go-test"]({ "args": ["-race"] });
}
```

## go-vet

**Command:** `go vet ./...`

### Example

<!-- run-recorder -->
```buzz
// go-vet is static analysis, so it composes into the canonical `lint` target
// (alongside golangci-lint) rather than a bespoke `vet` target. `magus run lint`
// forks `go vet ./...`.
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun lint(args: [str]) > void {
    go["go-vet"]();
}
```

## golangci-lint

**Command:** `go tool golangci-lint run ./...`

### debug

Appends `-v`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "-v"
  }
]
```

</details>

### rw

Inserts `--fix`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/3",
    "value": "--fix"
  }
]
```

</details>

### Example

<!-- run-recorder -->
```buzz
// golangci-lint runs as a `go tool` (resolved from go.mod's tool block). The rw
// charm inserts --fix, so `magus run lint:rw` applies the autofixable findings.
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun lint(args: [str]) > void {
    go["golangci-lint"]();
}
```

## govulncheck

Scans for known vulnerabilities in the module's call graph. Command as a `go tool` (like golangci-lint) so it resolves from go.mod's tool block, not PATH.

**Command:** `go tool govulncheck ./...`

### Example

<!-- run-recorder -->
```buzz
// govulncheck scans the module's call graph for known vulnerabilities, run as a
// `go tool` so it resolves from go.mod's tool block. Security scanning is static
// analysis, so it composes into the canonical `lint` target - not a bespoke
// `audit`/`security` target. (A slow scan can instead be gated in `ci`.)
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun lint(args: [str]) > void {
    go["govulncheck"]();
}
```

## scip

scip is the reserved op that runs the language's SCIP indexer for the knowledge graph. Named for the format, not the binary, so the same verb produces symbols across every language spell (`magus run <project>::scip`). magus injects MAGUS_SYMBOL_INDEX with the cache destination, so the index never lands in the tree; scip-go writes there via --output. Run through sh so the env var expands.

**Command:** `sh -c scip-go --output "$MAGUS_SYMBOL_INDEX"`

