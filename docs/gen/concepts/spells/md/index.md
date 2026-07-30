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

Every op is invoked as `md["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `md["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L179) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L183) |


Working directory and environment are NOT options: they ride the context, as `md["<op>"](ctx.withCwd("sub"))` and `md["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## markdownlint

**Command:** `markdownlint **/*.md **/*.mdx`

### Example

<!-- magus-run-recorder -->
```buzz
// markdownlint enforces Markdown style across the docs.
import "magus";
import "magus/spell/md";

magus\project({ "spells": [md] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    md["markdownlint"](ctx);
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

magus\project({ "spells": [md] });

export fun format(ctx: magus\Context, args: [str]) > void {
    md["prettier"](ctx);
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

