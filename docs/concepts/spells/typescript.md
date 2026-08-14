---
title: typescript spell
description: "TypeScript toolchain spell: tsc, eslint, prettier, and vitest run through the project package manager."
tags: [typescript, spell, node, eslint, vitest, tools]
---

# typescript

The `typescript` spell wires a TypeScript project's tooling into a magusfile, forking each tool through the project package manager (`pnpm exec`). It is an opaque spell: `preflight` composes the individual checks into one target.

**Runtime name:** `typescript` (source `spells/typescript/`)

**Version probe (node):** `node --version`

**Version probe (pnpm):** `pnpm --version`

**Version probe (tsc):** `pnpm exec tsc --version`

**Opaque:** yes (its outputs are not enumerable, so magus treats the whole workspace as the cache input).

## Passing arguments to ops

Every op is invoked as `typescript["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `typescript["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L168) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L172) |


Working directory and environment are NOT options: they ride the context, as `typescript["<op>"](ctx.withCwd("sub"))` and `typescript["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise; passing either as an option is an error.

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
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    typescript["biome-check"](ctx);
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
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun format(ctx: magus\Context, args: [str]) > void {
    typescript["biome-format"](ctx);
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
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun serve(ctx: magus\Context, args: [str]) > void {
    typescript["dev-server"](ctx);
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
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    typescript["eslint"](ctx);
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
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun preflight(ctx: magus\Context, args: [str]) > void {
    typescript["preflight"](ctx);
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
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun format(ctx: magus\Context, args: [str]) > void {
    typescript["prettier"](ctx);
}
```

## scip

scip is the reserved op that runs the TypeScript SCIP indexer for the knowledge graph. The indexer is a PATH binary (install it with mise, not as a project dep), so the op forks it directly. magus injects MAGUS_SYMBOL_INDEX with the cache destination, so the index never lands in the tree; scip-typescript writes there via --output. The runner resolves the bare $MAGUS_SYMBOL_INDEX token against that destination, so no shell is needed to expand it.

**Command:** `scip-typescript index --output $MAGUS_SYMBOL_INDEX`

## tsc

**Command:** `pnpm exec tsc`

### Example

<!-- magus-run-recorder -->
```buzz
// tsc is static analysis, so it composes into the canonical `lint` target
// (alongside eslint) rather than a bespoke `typecheck` target. `magus run lint`
// forks `pnpm exec tsc`.
import "magus";
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    typescript["tsc"](ctx);
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
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun build(ctx: magus\Context, args: [str]) > void {
    typescript["tsc-build"](ctx);
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
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun clean(ctx: magus\Context, args: [str]) > void {
    typescript["tsc-clean"](ctx);
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
import "magus/spell/typescript";

magus\project({ "spells": [typescript] });

export fun test(ctx: magus\Context, args: [str]) > void {
    typescript["vitest"](ctx);
}
```

