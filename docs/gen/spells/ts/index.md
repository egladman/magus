---
title: ts spell
description: "TypeScript toolchain spell: tsc, eslint, prettier, and vitest run through the project package manager."
tags: [ts, spell, typescript, node, eslint, vitest, tools]
---

# ts

The `ts` spell wires a TypeScript project's tooling into a magusfile, forking each tool through the project package manager (`pnpm exec`). It is an opaque spell: `preflight` composes the individual checks into one target.

**Runtime name:** `ts` (source `spells/typescript/`)

**Version probe:** `node --version`

**Provides:** `dist/**`

**Opaque:** yes (its outputs are not enumerable, so magus treats the whole workspace as the cache input).

## Passing arguments to ops

Every op is invoked as `ts["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `ts["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L108) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L105) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L112) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L120) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## biome-check

**Command:** `pnpm exec biome check .`

### gha

Inserts `--reporter=github`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/3",
    "value": "--reporter=github"
  }
]
```

</details>

### rw

Inserts `--write`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/3",
    "value": "--write"
  }
]
```

</details>

## biome-format

**Command:** `pnpm exec biome format .`

### rw

Inserts `--write`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/3",
    "value": "--write"
  }
]
```

</details>

## dev-server

**Command:** `pnpm run dev`

## eslint

**Command:** `pnpm exec eslint .`

### gha

Inserts `--format=unix`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/2",
    "value": "--format=unix"
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
    "path": "/2",
    "value": "--fix"
  }
]
```

</details>

## preflight

**Command:** none; this op composes the spell's other ops (see the intro).

## prettier

**Command:** `pnpm exec prettier --check .`

### rw

Replaces `--check` with `--write`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "replace",
    "path": "/2",
    "value": "--write"
  }
]
```

</details>

## scip

**Command:** `sh -c scip-typescript index --output "$MAGUS_SYMBOL_INDEX"`

## tsc

**Command:** `pnpm exec tsc`

## tsc-build

**Command:** `pnpm exec tsc --build`

## tsc-clean

**Command:** `pnpm exec tsc --build --clean`

## vitest

**Command:** `pnpm exec vitest run`

### gha

Appends `--reporter=github-actions`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "--reporter=github-actions"
  }
]
```

</details>

