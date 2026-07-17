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
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `buf["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L108) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L105) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L112) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L120) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## buf-breaking

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

## buf-build

**Command:** `buf build`

## buf-format

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

## buf-generate

**Command:** `buf generate`

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

