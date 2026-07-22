---
title: py spell
description: "Python toolchain spell: pytest, ruff check/format, and uv build/clean as magus ops."
tags: [py, spell, python, uv, pytest, ruff, tools]
---

# py

The `py` spell wires a Python project's tooling into a magusfile through `uv`. Tests, linting (ruff), and formatting run as `uv run` subcommands so they resolve from the project's locked environment.

**Runtime name:** `py` (source `spells/python/`)

**Version probe:** `python3 --version`

## Passing arguments to ops

Every op is invoked as `py["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `py["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L108) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L105) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L112) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L120) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

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
import "magus/spell/py";

magus.project({ "spells": [py] });

export fun test(ctx: magus\Context, args: [str]) > void {
    py["pytest"]({ "args": ["-k", "integration"] });
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
import "magus/spell/py";

magus.project({ "spells": [py] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    py["ruff-check"]();
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
import "magus/spell/py";

magus.project({ "spells": [py] });

export fun format(ctx: magus\Context, args: [str]) > void {
    py["ruff-format"]();
}
```

## scip

scip is the reserved op that runs the Python SCIP indexer for the knowledge graph. The indexer is a PATH binary (install it with mise, not as a project dep), so the op forks it directly. magus injects MAGUS_SYMBOL_INDEX with the cache destination, so the index never lands in the tree; scip-python writes there via --output. Run through sh so the env var expands.

**Command:** `sh -c scip-python index . --output "$MAGUS_SYMBOL_INDEX"`

## uv-build

build/clean are uv's own subcommands; pytest and ruff are tools uv merely runs, so they are named after the tool (pytest, ruff-check), not the `uv run` wrapper.

**Command:** `uv build`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire uv-build into a build target: magus run build forks uv build.
import "magus";
import "magus/spell/py";

magus.project({ "spells": [py] });

export fun build(ctx: magus\Context, args: [str]) > void {
    py["uv-build"]();
}
```

## uv-clean

**Command:** `uv clean`

### Example

<!-- magus-run-recorder -->
```buzz
// Wire uv-clean into a clean target: magus run clean forks uv clean.
import "magus";
import "magus/spell/py";

magus.project({ "spells": [py] });

export fun clean(ctx: magus\Context, args: [str]) > void {
    py["uv-clean"]();
}
```

