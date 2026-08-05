---
title: protobuf spell
description: "Protobuf spell: buf build, lint, format, and code generation from .proto sources."
tags: [protobuf, spell, buf, codegen, lint, tools]
---

# protobuf

The `protobuf` spell forks the `buf` CLI to build, lint, format, and generate from Protobuf sources. It declares `gen/**` as its outputs so magus caches generated code correctly. It is named for the domain rather than for `buf`, leaving room for a second protobuf tool.

**Runtime name:** `protobuf` (source `spells/protobuf/`)

**Version probe:** `buf --version`

**Provides:** `gen/**`

## Passing arguments to ops

Every op is invoked as `protobuf["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `protobuf["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L233) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L237) |


Working directory and environment are NOT options: they ride the context, as `protobuf["<op>"](ctx.withCwd("sub"))` and `protobuf["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## buf-breaking

breaking checks the current schema against a baseline for backward-incompatible changes (wire and JSON compatibility). The baseline is REQUIRED and the caller names it - buf errors clearly without --against, and there is no default here on purpose: a hardcoded `.git#branch=main` failed every master/trunk repo out of the box, and shallow CI clones (fetch-depth: 1) cannot resolve a branch ref at all, so the right baseline (a branch, a tag, a BSR image) is the magusfile's fact: protobuf["buf-breaking"](ctx, {"args": ["--against", ".git#branch=main"]}); This is the protobuf analogue of an API-contract gate: compose it into `lint` so a breaking .proto change fails the same read-only stage as go-vet and golangci-lint. The gha charm swaps buf's reporter to GitHub Actions annotations.

**Command:** `buf breaking`

### gha

Appends `--error-format=github-actions`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "--error-format=github-actions"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// buf-breaking gates backward-incompatible schema changes, so it composes into the
// read-only `lint` target alongside buf-lint. The magusfile names the baseline:
// --against is required, and a branch ref like `.git#branch=main` assumes a full
// clone (a shallow CI checkout with fetch-depth: 1 cannot resolve it - fetch the
// baseline ref, or compare against a BSR image instead). A wire- or JSON-
// incompatible .proto edit then fails lint the same way go-vet fails a
// static-analysis violation.
import "magus";
import "magus/spell/protobuf";

magus\project({ "spells": [protobuf] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    protobuf["buf-lint"](ctx);
    protobuf["buf-breaking"](ctx, {"args": ["--against", ".git#branch=main"]});
}
```

## buf-build

**Command:** `buf build`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire buf-build into a build target: magus run build forks buf build.
import "magus";
import "magus/spell/protobuf";

magus\project({ "spells": [protobuf] });

export fun build(ctx: magus\Context, args: [str]) > void {
    protobuf["buf-build"](ctx);
}
```

## buf-dep-update

dep-update maintains the buf.lock this spell already declares as an input - without it the lockfile was a file the spell could invalidate on but never update. Unlike go-mod-tidy there is no read-only diff mode to default to (buf dep update always writes), so compose it into an rw-gated target and let the generate drift gate catch a stale lockfile.

**Command:** `buf dep update`

## buf-format

format checks by default: -d prints a diff of what would change (without it, buf dumps every formatted file in full to stdout - an unreadable CI log on failure) and --exit-code fails the run when that diff is non-empty. The write charm applies the formatting in place. The patch replaces --exit-code (the higher index) before dropping -d, so the concatenated ops apply sequentially without an index shifting out from under them.

**Command:** `buf format -d --exit-code`

### rw

Replaces `--exit-code` with `-w`, drops `-d`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "replace",
    "path": "/2",
    "value": "-w"
  },
  {
    "op": "remove",
    "path": "/1"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// buf-format checks formatting; the rw charm (magus run format:rw) rewrites in place.
import "magus";
import "magus/spell/protobuf";

magus\project({ "spells": [protobuf] });

export fun format(ctx: magus\Context, args: [str]) > void {
    protobuf["buf-format"](ctx);
}
```

## buf-generate

**Command:** `buf generate`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire buf-generate into a generate target: magus run generate forks buf generate.
import "magus";
import "magus/spell/protobuf";

magus\project({ "spells": [protobuf] });

export fun generate(ctx: magus\Context, args: [str]) > void {
    protobuf["buf-generate"](ctx);
}
```

## buf-lint

**Command:** `buf lint`

### gha

Appends `--error-format=github-actions`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "--error-format=github-actions"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// buf-lint checks Protobuf style. The gha charm annotates findings in GitHub Actions.
import "magus";
import "magus/spell/protobuf";

magus\project({ "spells": [protobuf] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    protobuf["buf-lint"](ctx);
}
```

## buf-push

push publishes the module to its configured BSR; what and where come from buf.yaml, credentials from `buf registry login`.

**Command:** `buf push`

