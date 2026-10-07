---
title: vale spell
generated_from: spells/vale/spell.buzz
description: "Vale spell: judge prose that a target extracts against a .vale.ini's styles."
tags: [vale, spell, prose, lint, tools]
---

# vale

The `vale` spell forks the Vale prose linter and returns its JSON report, so a target maps each alert back to the text it extracted: comment blocks, commit messages, pull request text. It claims no source globs, and its probed version keys the cache, so upgrading Vale reruns only the targets that call it.

**Runtime name:** `vale` (source `spells/vale/`)

**Version probe (vale):** `vale --version`

## Passing arguments to ops

Every op is invoked as `vale["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key     | Type    | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | Source                                                                                              |
| ------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| `args`  | `[str]` | Extra arguments appended to the resolved command, replacing any trailing defaults the op declares (go-test's `./...`), so passing args also states the scope. Omit it and a bare `vale["<op>"]()` keeps the defaults and forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. To keep the passthrough too, append the target's own `args` parameter: `{"args": ["-race", "./..."] + args}`. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L190) |
| `stdin` | `str`   | Data written to the command's standard input.                                                                                                                                                                                                                                                                                                                                                                                                                                        | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L194) |


Working directory and environment are NOT options: they ride the context, as `vale["<op>"](ctx.withCwd("sub"))` and `vale["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise; passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## vale

--no-exit: an alert is the report, not a failed run; the caller decides what blocks.

**Command:** `vale --output=JSON --no-exit <args>`

