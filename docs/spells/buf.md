---
title: buf spell
description: "Buf spell: protobuf build, lint, format, and code generation."
tags: [buf, spell, protobuf, codegen, lint, tools]
---

# buf

The `buf` spell forks the `buf` CLI to build, lint, format, and generate from Protobuf sources. It declares `gen/**` as its outputs so magus caches generated code correctly.

**Runtime name:** `buf` (source `spells/buf/`)

**Version probe:** `buf --version`

**Provides:** `gen/**`

## Passing arguments to ops

Every op is invoked as `buf["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `buf["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L107) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L104) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L111) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L119) |

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

<!-- run-recorder -->
```buzz
// buf-breaking gates backward-incompatible schema changes, so it composes into the
// read-only `lint` target alongside buf-lint. `magus run lint` forks `buf lint` then
// `buf breaking --against .git#branch=main`, failing on a wire- or JSON-incompatible
// .proto edit the same way go-vet fails a static-analysis violation.
import "magus";
import "magus/spell/buf";

magus.project({ "spells": [buf] });

export fun lint(args: [str]) > void {
    buf["buf-lint"]();
    buf["buf-breaking"]();
}
```

## buf-build

**Command:** `buf build`

### Example

<!-- run-recorder -->
```buzz
// Wire buf-build into a build target: magus run build forks buf build.
import "magus";
import "magus/spell/buf";

magus.project({ "spells": [buf] });

export fun build(args: [str]) > void {
    buf["buf-build"]();
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

<!-- run-recorder -->
```buzz
// buf-format checks formatting; the rw charm (magus run format:rw) rewrites in place.
import "magus";
import "magus/spell/buf";

magus.project({ "spells": [buf] });

export fun format(args: [str]) > void {
    buf["buf-format"]();
}
```

## buf-generate

**Command:** `buf generate`

### Example

<!-- run-recorder -->
```buzz
// Wire buf-generate into a generate target: magus run generate forks buf generate.
import "magus";
import "magus/spell/buf";

magus.project({ "spells": [buf] });

export fun generate(args: [str]) > void {
    buf["buf-generate"]();
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

<!-- run-recorder -->
```buzz
// buf-lint checks Protobuf style. The gha charm annotates findings in GitHub Actions.
import "magus";
import "magus/spell/buf";

magus.project({ "spells": [buf] });

export fun lint(args: [str]) > void {
    buf["buf-lint"]();
}
```

