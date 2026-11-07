---
title: latex spell
generated_from: spells/latex/spell.buzz
description: "LaTeX spell: tectonic builds the PDF, chktex lints, and latexindent formats."
tags: [latex, spell, tex, pdf, lint, tools]
---

# latex

The `latex` spell builds a LaTeX project to a PDF with `tectonic`, lints it with `chktex`, and formats it with `latexindent`; the `rw` charm turns the format check into an in-place rewrite. The first build on a machine fetches the TeX bundle its `Tectonic.toml` names over the network, and later builds run from tectonic's cache. mise pins tectonic and latexindent; chktex has no mise backend and no Homebrew formula, so install it from a TeX distribution. chktex exits 2 on any warning, so `lint` fails on one.

**Runtime name:** `latex` (source `spells/latex/`)

**Version probe (chktex):** `chktex --version`

**Version probe (latexindent):** `latexindent --version`

**Version probe (tectonic):** `tectonic --version`

**Provides:** `build/**`

## Passing arguments to ops

Every op is invoked as `latex["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key     | Type    | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                           | Source                                                                                         |
| ------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `args`  | `[str]` | Extra arguments appended to the resolved command, replacing any trailing defaults the op declares (go-test's `./...`), so passing args also states the scope. Omit it and a bare `latex["<op>"]()` keeps the defaults and forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. To keep the passthrough too, append the target's own `args` parameter: `{"args": ["-race", "./..."] + args}`. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go) |
| `stdin` | `str`   | Data written to the command's standard input.                                                                                                                                                                                                                                                                                                                                                                                                                                         | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go) |


Working directory and environment are NOT options: they ride the context, as `latex["<op>"](ctx.withCwd("sub"))` and `latex["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise; passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## chktex

chktex exits 2 on any warning (checked on 1.7.9), so lint fails on one. -q drops only the banner.

**Command:** `chktex -q`

### Example

<!-- magus-run-recorder -->
```buzz
// chktex lints the project's LaTeX sources.
import "magus";
import "magus/spell/latex";

magus\project({ "spells": [latex] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    latex["chktex"](ctx);
}
```

## latexindent

-k exits 1 when indenting would change the file; rw swaps it for -wd. -g keeps indent.log out of the tree. -c moves the backup into build/.

**Command:** `latexindent -k -s -g=/dev/null -c=build/latexindent`

### rw

Replaces `-k` with `-wd`.

<details class="charm-patch">
<summary>JSON Patch</summary>

```json
[
  {
    "op": "replace",
    "path": "/0",
    "value": "-wd"
  }
]
```

</details>

### Example

<!-- magus-run-recorder -->
```buzz
// latexindent checks the project's LaTeX indentation.
import "magus";
import "magus/spell/latex";

magus\project({ "spells": [latex] });

export fun format(ctx: magus\Context, args: [str]) > void {
    latex["latexindent"](ctx);
}
```

## tectonic

The bare form needs its output directory to exist, which a static argv cannot ensure. -X reads outputs and inputs from Tectonic.toml.

**Command:** `tectonic -X build`

### Example

<!-- magus-run-recorder -->
```buzz
// tectonic builds the PDF a Tectonic.toml declares.
import "magus";
import "magus/spell/latex";

magus\project({ "spells": [latex] });

export fun build(ctx: magus\Context, args: [str]) > void {
    latex["tectonic"](ctx);
}
```

