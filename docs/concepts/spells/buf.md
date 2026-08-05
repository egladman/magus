---
title: buf spell
description: "Buf spell: protobuf build, lint, format, and code generation."
tags: [buf, spell, protobuf, codegen, lint, tools]
---

# buf

The `buf` spell forks the `buf` CLI to build, lint, format, and generate from Protobuf sources. It declares `gen/**` as its outputs so magus caches generated code correctly.

**Runtime name:** `buf` (source `spells/buf/`)

**Version probe (buf):** `buf --version`

**Provides:** `gen/**`

## Passing arguments to ops

Every op is invoked as `buf["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `buf["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L169) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L173) |


Working directory and environment are NOT options: they ride the context, as `buf["<op>"](ctx.withCwd("sub"))` and `buf["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## buf-breaking

breaking checks the current schema against a baseline for backward-incompatible changes (wire and JSON compatibility). It defaults to comparing against the main branch, buf's standard CI pattern; point it elsewhere with a function target when a repo uses a different default branch or an image baseline. This is the protobuf analogue of an API-contract gate: compose it into `lint` so a breaking .proto change fails the same read-only stage as go-vet and golangci-lint. The gha charm swaps buf's reporter to GitHub Actions annotations.

**Command:** `buf breaking --against .git#branch=main`

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
// read-only `lint` target alongside buf-lint. `magus run lint` forks `buf lint` then
// `buf breaking --against .git#branch=main`, failing on a wire- or JSON-incompatible
// .proto edit the same way go-vet fails a static-analysis violation.
import "magus";
import "magus/spell/buf";

magus\project({ "spells": [buf] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    buf["buf-lint"](ctx);
    buf["buf-breaking"](ctx);
}
```

## buf-build

**Command:** `buf build`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire buf-build into a build target: magus run build forks buf build.
import "magus";
import "magus/spell/buf";

magus\project({ "spells": [buf] });

export fun build(ctx: magus\Context, args: [str]) > void {
    buf["buf-build"](ctx);
}
```

## buf-format

format checks by default (--exit-code fails CI when files would change; the write charm applies the formatting in place.

**Command:** `buf format --exit-code`

### rw

Replaces `--exit-code` with `-w`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "replace",
    "path": "/1",
    "value": "-w"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// buf-format checks formatting; the rw charm (magus run format:rw) rewrites in place.
import "magus";
import "magus/spell/buf";

magus\project({ "spells": [buf] });

export fun format(ctx: magus\Context, args: [str]) > void {
    buf["buf-format"](ctx);
}
```

## buf-generate

**Command:** `buf generate`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire buf-generate into a generate target: magus run generate forks buf generate.
import "magus";
import "magus/spell/buf";

magus\project({ "spells": [buf] });

export fun generate(ctx: magus\Context, args: [str]) > void {
    buf["buf-generate"](ctx);
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
import "magus/spell/buf";

magus\project({ "spells": [buf] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    buf["buf-lint"](ctx);
}
```

