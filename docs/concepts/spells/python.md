---
title: python spell
aliases: [concepts/spells/py]
description: "Python toolchain spell: pytest, ruff check/format, mypy, pyright, black, pip-audit, and uv build/sync as magus ops."
tags: [python, spell, uv, pytest, ruff, tools]
---

# python

The `python` spell wires a Python project's tooling into a magusfile through `uv`. Tests, linting (ruff), type-checking (mypy or pyright), and formatting (ruff-format or black) run as `uv run` subcommands so they resolve from the project's locked environment; uv-sync installs that environment for a preflight target.

**Runtime name:** `python` (source `spells/python/`)

**Version probe:** `python3 --version`

**Named probes:** `uv` (`uv --version`) - each records UNPROBED when the tool is absent, and moves the cache key when installed.

## Passing arguments to ops

Every op is invoked as `python["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `python["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L233) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L237) |


Working directory and environment are NOT options: they ride the context, as `python["<op>"](ctx.withCwd("sub"))` and `python["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## black

black is the mainstream formatter alternative to ruff-format, offered per the eslint-vs-biome pattern; the magusfile picks one. Base --check is read-only; rw drops it to write, mirroring ruffFormat.

**Command:** `uv run black --check .`

### rw

Drops `--check`.

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

## mypy

mypy and pyright are the two mainstream type checkers - the magusfile picks which composes into lint (ruff does not type-check). Both resolve from the locked environment like pytest and ruff.

**Command:** `uv run mypy .`

### Example

<!-- magus-run-recorder -->
```buzz
// mypy type-checks, which is static analysis, so it composes into the canonical
// `lint` target alongside ruff-check (ruff does not type-check). pyright is the
// other mainstream checker - the magusfile picks one, not this spell.
import "magus";
import "magus/spell/python";

magus\project({ "spells": [python] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    python["ruff-check"](ctx);
    python["mypy"](ctx);
}
```

## pip-audit

pip-audit scans the project's dependencies against the PyPI advisory database. It resolves from the locked environment like pytest and ruff (add it as a dev dependency), and composes into the canonical `security` target - its verdict tracks the advisory database, not the tree, so pair the target with skip_cache (see targets.md).

**Command:** `uv run pip-audit`

### Example

<!-- magus-run-recorder -->
```buzz
// pip-audit checks the locked dependencies against the PyPI advisory database,
// so it composes into the canonical `security` target. The verdict tracks the
// advisory database, not the tree - declare skip_cache on the target so a
// replayed pass cannot hide a newly published CVE.
import "magus";
import "magus/spell/python";

magus\project({ "spells": [python], "targets": {
    "security": {"skip_cache": "reads the PyPI advisory database, which changes independently of this tree"},
} });

export fun security(ctx: magus\Context, args: [str]) > void {
    python["pip-audit"](ctx);
}
```

## pyright

**Command:** `uv run pyright`

## pytest

**Command:** `uv run pytest`

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
// pytest runs the suite via `uv run`; here filtered to tests matching a keyword,
// so `magus run test` forks `uv run pytest -k integration`. The debug charm
// (`magus run test:debug`) adds -v.
import "magus";
import "magus/spell/python";

magus\project({ "spells": [python] });

export fun test(ctx: magus\Context, args: [str]) > void {
    python["pytest"](ctx, { "args": ["-k", "integration"] });
}
```

## ruff-check

**Command:** `uv run ruff check .`

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

### gha

Inserts `--output-format=github`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/3",
    "value": "--output-format=github"
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

### Example

<!-- magus-run-recorder -->
```buzz
// ruff-check lints via uv run ruff; the rw charm autofixes, gha annotates in CI.
import "magus";
import "magus/spell/python";

magus\project({ "spells": [python] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    python["ruff-check"](ctx);
}
```

## ruff-format

**Command:** `uv run ruff format --check .`

### rw

Drops `--check`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "remove",
    "path": "/3"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// ruff-format checks formatting; the rw charm (magus run format:rw) rewrites in place.
import "magus";
import "magus/spell/python";

magus\project({ "spells": [python] });

export fun format(ctx: magus\Context, args: [str]) > void {
    python["ruff-format"](ctx);
}
```

## scip

scip is the reserved op that runs the Python SCIP indexer for the knowledge graph. The indexer is a PATH binary (install it with mise, not as a project dep), so the op forks it directly. magus injects MAGUS_SYMBOL_INDEX with the cache destination, so the index never lands in the tree; scip-python writes there via --output. Run through sh so the env var expands.

**Command:** `sh -c scip-python index . --output "$MAGUS_SYMBOL_INDEX"`

## uv-build

build/sync/publish are uv's own subcommands; pytest, ruff, mypy, pyright, and black are tools uv merely runs, so they are named after the tool (pytest, ruff-check), not the `uv run` wrapper. There is deliberately no clean op. uv has no clean verb (`uv clean` is not a subcommand; `uv cache clean` clears the GLOBAL cache, not project artifacts), and where build output lands is the build backend's choice, not this spell's to guess - the same lesson as typescript's empty provided globs. A magusfile that wants `magus run clean` composes its own removal of the dirs its backend actually writes.

**Command:** `uv build`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire uv-build into a build target: magus run build forks uv build.
import "magus";
import "magus/spell/python";

magus\project({ "spells": [python] });

export fun build(ctx: magus\Context, args: [str]) > void {
    python["uv-build"](ctx);
}
```

## uv-publish

uv-publish uploads dist/ artifacts to the configured index; credentials and index selection are the caller's args/environment.

**Command:** `uv publish`

## uv-sync

uv-sync installs the locked environment - the real op behind a preflight target. Base --frozen fails when uv.lock is stale (CI-strict); rw drops it so a local sync may update the lockfile.

**Command:** `uv sync --frozen`

### rw

Drops `--frozen`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
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
// uv-sync installs the locked environment, giving `preflight` something real to
// do: the base --frozen form fails on a stale uv.lock (CI-strict), and
// `preflight:rw` may update the lockfile locally.
import "magus";
import "magus/spell/python";

magus\project({ "spells": [python] });

export fun preflight(ctx: magus\Context, args: [str]) > void {
    python["uv-sync"](ctx);
}
```

