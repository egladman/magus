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

biome-check is biome's lint/analyze pass (eslint's role, if your project chose Biome over eslint+prettier - the magusfile decides which composes into lint, not this spell). --write and --reporter=github verified against the current Biome CLI docs (biomejs.dev/reference/cli).

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

### Example

<!-- magus-run-recorder -->
```buzz
// biome-check lints the project through Biome (pnpm exec biome check), the
// spell's alternative to eslint - the magusfile picks one, not both.
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    ts["biome-check"]();
}
```

## biome-format

biome-format is biome's formatter (prettier's role). Unlike prettier/ruffFormat, `biome format` has no --check flag to drop: it is read-only by default (reports differences, writes nothing) and --write applies them, so rw ADDS a flag instead of removing one.

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

### Example

<!-- magus-run-recorder -->
```buzz
// biome-format checks formatting through Biome (pnpm exec biome format); the
// rw charm (magus run format:rw) applies --write. The spell's alternative to
// prettier - the magusfile picks one, not both.
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun format(ctx: magus\Context, args: [str]) > void {
    ts["biome-format"]();
}
```

## dev-server

dev-server runs the project's package.json "dev" script via the package manager - framework-neutral (Vite, Next, webpack-dev-server, ...). No readiness probe is declared: the port and startup signal vary by framework, so guessing one would be wrong more often than right (readiness is optional - see services.md). A magusfile that needs to block on readiness for its specific dev server can declare its own service op instead.

**Command:** `pnpm run dev`

### Example

<!-- magus-run-recorder -->
```buzz
// dev-server runs the project's package.json dev script (pnpm run dev) as a
// supervised background process when reached via magus.needs, or foreground
// when run directly.
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun serve(ctx: magus\Context, args: [str]) > void {
    ts["dev-server"]();
}
```

## eslint

eslint has no built-in "github" formatter (unlike ruff's --output-format=github); "unix" is the built-in, no-extra-devDependency formatter closest to a CI-friendly, one-line-per-problem shape for annotation/regex parsing.

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

### Example

<!-- magus-run-recorder -->
```buzz
// eslint lints the project through the package manager (pnpm exec eslint).
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    ts["eslint"]();
}
```

## preflight

preflight is a no-op marker op (no command).

**Command:** none; this op composes the spell's other ops (see the intro).

### Example

<!-- magus-run-recorder -->
```buzz
// preflight composes the tsc/eslint/prettier/vitest checks into one opaque target.
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun preflight(ctx: magus\Context, args: [str]) > void {
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

<!-- magus-run-recorder -->
```buzz
// prettier checks formatting; the rw charm (magus run format:rw) rewrites in place.
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun format(ctx: magus\Context, args: [str]) > void {
    ts["prettier"]();
}
```

## scip

scip is the reserved op that runs the TypeScript SCIP indexer for the knowledge graph. The indexer is a PATH binary (install it with mise, not as a project dep), so the op forks it directly. magus injects MAGUS_SYMBOL_INDEX with the cache destination, so the index never lands in the tree; scip-typescript writes there via --output. Run through sh so the env var expands.

**Command:** `sh -c scip-typescript index --output "$MAGUS_SYMBOL_INDEX"`

## tsc

**Command:** `pnpm exec tsc`

### Example

<!-- magus-run-recorder -->
```buzz
// tsc is static analysis, so it composes into the canonical `lint` target
// (alongside eslint) rather than a bespoke `typecheck` target. `magus run lint`
// forks `pnpm exec tsc`.
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    ts["tsc"]();
}
```

## tsc-build

tsc-build uses TypeScript's project-references incremental build mode (works even without declared references, via its own .tsbuildinfo cache), emitting per tsconfig outDir - see mgs_listProvidedGlobs.

**Command:** `pnpm exec tsc --build`

### Example

<!-- magus-run-recorder -->
```buzz
// tsc-build compiles the project via TypeScript's project-references
// incremental build mode (pnpm exec tsc --build).
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun build(ctx: magus\Context, args: [str]) > void {
    ts["tsc-build"]();
}
```

## tsc-clean

tsc-clean mirrors tsc-build's project-references mode: --clean removes the declared outputs and the incremental .tsbuildinfo cache.

**Command:** `pnpm exec tsc --build --clean`

### Example

<!-- magus-run-recorder -->
```buzz
// tsc-clean removes tsc-build's declared outputs and its incremental
// .tsbuildinfo cache (pnpm exec tsc --build --clean).
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun clean(ctx: magus\Context, args: [str]) > void {
    ts["tsc-clean"]();
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

<!-- magus-run-recorder -->
```buzz
// vitest runs the test suite; the gha charm annotates failures in GitHub Actions.
import "magus";
import "magus/spell/ts";

magus.project({ "spells": [ts] });

export fun test(ctx: magus\Context, args: [str]) > void {
    ts["vitest"]();
}
```

