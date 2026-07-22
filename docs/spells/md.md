---
title: md spell
description: "Markdown docs spell: markdownlint and prettier for linting and formatting prose."
tags: [md, spell, markdown, docs, prettier, lint, tools]
---

# md

The `md` spell lints and formats Markdown. `markdownlint` enforces style, and `prettier` checks formatting; the `rw` charm turns the check into an in-place rewrite.

**Runtime name:** `md` (source `spells/markdown/`)

**Version probe:** none

## Passing arguments to ops

Every op is invoked as `md["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `md["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L108) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L105) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L112) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L120) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## markdownlint

**Command:** `markdownlint **/*.md **/*.mdx`

### Example

<!-- magus-run-recorder -->
```buzz
// markdownlint enforces Markdown style across the docs.
import "magus";
import "magus/spell/md";

magus.project({ "spells": [md] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    md["markdownlint"]();
}
```

## prettier

**Command:** `prettier --check --no-error-on-unmatched-pattern **/*.md **/*.mdx`

### rw

Replaces `--check` with `--write`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "replace",
    "path": "/0",
    "value": "--write"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// prettier checks Markdown formatting; the rw charm (magus run format:rw) rewrites in place.
import "magus";
import "magus/spell/md";

magus.project({ "spells": [md] });

export fun format(ctx: magus\Context, args: [str]) > void {
    md["prettier"]();
}
```

## typos

**Command:** `typos --format brief`

### rw

Appends `-w`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "add",
    "path": "/-",
    "value": "-w"
  }
]
```

</details>

