---
title: go spell
description: "Go toolchain spell: build, test, vet, fmt, mod-tidy, golangci-lint, and govulncheck as magus ops."
tags: [go, spell, golang, build, test, lint, tools]
---

# go

The `go` spell wires the Go toolchain into a magusfile: each op forks a `go` (or `gofmt`) subcommand directly, with no shell. Lint and vulnerability scanning run as `go tool` invocations so they resolve from the module's tool block rather than PATH.

**Runtime name:** `go` (source `spells/golang/`)

**Version probe (go):** `go version`

**Version probe (golangci-lint):** `golangci-lint --version`

**Version probe (govulncheck):** `govulncheck -version`

## Passing arguments to ops

Every op is invoked as `go["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `go["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L169) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L173) |


Working directory and environment are NOT options: they ride the context, as `go["<op>"](ctx.withCwd("sub"))` and `go["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## go-build

**Command:** `go build`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire go-build into a `build` target. `magus run build` forks `go build`.
import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun build(ctx: magus\Context, args: [str]) > void {
    go["go-build"](ctx);
}
```

## go-clean

**Command:** `go clean ./...`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire go-clean into a `clean` target: `magus run clean` forks `go clean ./...`.
import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun clean(ctx: magus\Context, args: [str]) > void {
    go["go-clean"](ctx);
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

<!-- magus-run-recorder -->
```buzz
// go-fmt lists misformatted files; the rw charm rewrites them in place.
// `magus run format` checks, `magus run format:rw` applies gofmt.
import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun format(ctx: magus\Context, args: [str]) > void {
    go["go-fmt"](ctx);
}
```

## go-generate

**Command:** `go generate ./...`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire go-generate into a `generate` target: `magus run generate` forks
// `go generate ./...`.
import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun generate(ctx: magus\Context, args: [str]) > void {
    go["go-generate"](ctx);
}
```

## go-mod-edit

**Command:** `go mod edit -print`

### rw

Drops `-print`.

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

## go-mod-json

Captures Go's structured module view for the spell's higher-level Buzz helper. This is deliberately a separate read-only op: `-json` and `-print` are distinct Go modes, while go-mod-edit remains the one command that applies derived edits.

**Command:** `go mod edit -json`

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

### Example

<!-- magus-run-recorder -->
```buzz
// go-mod-tidy checks go.mod/go.sum with --diff (CI-safe); the rw charm drops
// --diff so `magus run tidy:rw` applies the changes.
import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun tidy(ctx: magus\Context, args: [str]) > void {
    go["go-mod-tidy"](ctx);
}
```

## go-run

**Command:** `go run`

### Example

<!-- magus-run-recorder -->
```buzz
// Run a repo-local Go tool through the spell instead of os.exec. go-run has no
// useful bare form: name the package and its flags via the "args" option, which
// append after `go run`. This forks `go run ./cmd/gen-docs -out ./docs`.
import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun generate(ctx: magus\Context, args: [str]) > void {
    go["go-run"](ctx, {"args": ["./cmd/gen-docs", "-out", "./docs"]});
}
```

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

### Example

<!-- magus-run-recorder -->
```buzz
// go-test runs the suite; here with the race detector, so `magus run test` forks
// `go test ./... -race`. The cd charm (`magus run test:cd`) adds the atomic
// coverage profile a CD pipeline ships.
import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun test(ctx: magus\Context, args: [str]) > void {
    go["go-test"](ctx, { "args": ["-race"] });
}
```

## go-vet

**Command:** `go vet ./...`

### Example

<!-- magus-run-recorder -->
```buzz
// go-vet is static analysis, so it composes into the canonical `lint` target
// (alongside golangci-lint) rather than a bespoke `vet` target. `magus run lint`
// forks `go vet ./...`.
import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    go["go-vet"](ctx);
}
```

## golangci-lint

Invoked directly rather than through `go tool`: golangci-lint generates no code, so it has none of the generator/runtime lockstep that keeps protoc-gen-go pinned in go.mod. `go tool golangci-lint` also required the binary in the module's tool block, and a workspace that had not put it there got "no such tool" - the op could not run at all. On PATH it is pinned by whatever the workspace uses (mise, asdf, a system package), and the spell's version probe records which.

**Command:** `golangci-lint run ./...`

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
    "path": "/1",
    "value": "--fix"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// golangci-lint runs as a `go tool` (resolved from go.mod's tool block). The rw
// charm inserts --fix, so `magus run lint:rw` applies the autofixable findings.
import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    go["golangci-lint"](ctx);
}
```

## govulncheck

Invoked directly rather than through `go tool`, for the same reason as golangCILint above: `go tool govulncheck` requires the binary in the module's tool block, and a workspace that had not put it there got "no such tool" - so the op could not run at all. On PATH it is pinned by whatever the workspace uses, and mgs_getVersionProbes records which.

**Command:** `govulncheck ./...`

### Example

<!-- magus-run-recorder -->
```buzz
// govulncheck scans the module's call graph for known vulnerabilities, run as a
// `go tool` so it resolves from go.mod's tool block. Security scanning is static
// analysis, so it composes into the canonical `lint` target - not a bespoke
// `audit`/`security` target. (A slow scan can instead be gated in `ci`.)
import "magus";
import "magus/spell/go";

magus\project({ "spells": [go] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    go["govulncheck"](ctx);
}
```

## scip

**Command:** `sh -c scip-go --output "$MAGUS_SYMBOL_INDEX"`

