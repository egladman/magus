---
title: ts spell
description: "TypeScript toolchain spell: tsc, eslint, prettier, and vitest run through the project package manager."
tags: [ts, spell, typescript, node, eslint, vitest, tools]
---

# ts

The `ts` spell wires a TypeScript project's tooling into a magusfile, forking each tool through the project package manager (`pnpm exec`). It is an opaque spell: `preflight` composes the individual checks into one target.

**Runtime name:** `ts` (source `spells/typescript/`)

**Version probe:** `node --version`

**Opaque:** yes (its outputs are not enumerable, so magus treats the whole workspace as the cache input).

## Passing arguments to ops

Every op is invoked as `ts["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `ts["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L107) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L104) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L111) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L119) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## eslint

**Command:** `pnpm exec eslint .`

### Example

<!-- run-recorder -->
```buzz
// eslint lints the project through the package manager (pnpm exec eslint).
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun lint(args: [str]) > void {
    ts["eslint"]();
}
```

## preflight

preflight is a no-op marker op (no command).

**Command:** none; this op composes the spell's other ops (see the intro).

### Example

<!-- run-recorder -->
```buzz
// preflight composes the tsc/eslint/prettier/vitest checks into one opaque target.
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun preflight(args: [str]) > void {
    ts["preflight"]();
}
```

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

### Example

<!-- run-recorder -->
```buzz
// prettier checks formatting; the rw charm (magus run format:rw) rewrites in place.
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun format(args: [str]) > void {
    ts["prettier"]();
}
```

## tsc

**Command:** `pnpm exec tsc`

### Example

<!-- run-recorder -->
```buzz
// tsc type-checks the project through the package manager (pnpm exec tsc).
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun typecheck(args: [str]) > void {
    ts["tsc"]();
}
```

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

### Example

<!-- run-recorder -->
```buzz
// vitest runs the test suite; the gha charm annotates failures in GitHub Actions.
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun test(args: [str]) > void {
    ts["vitest"]();
}
```

