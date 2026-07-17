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

## go-clean

**Command:** `go clean ./...`

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

## go-generate

**Command:** `go generate ./...`

## go-mod-tidy

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

## go-test

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

## go-vet

**Command:** `go vet ./...`

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

## govulncheck

**Command:** `go tool govulncheck ./...`

## scip

**Command:** `sh -c scip-go --output "$MAGUS_SYMBOL_INDEX"`

