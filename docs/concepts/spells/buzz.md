---
title: buzz spell
generated_from: spells/buzz/spell.buzz
description: "Buzz language identity: the .buzz extension and the comment and string syntax magus reads source with."
tags: [buzz, spell, language, tools]
---

# buzz

The `buzz` spell declares what a Buzz file IS and declares no ops. It is what lets the knowledge graph and the symbol index tell code from a comment or a string in a `.buzz` source, and what binds the extension to its project. Running Buzz needs no spell: `magus buzz -t <file>` executes a file's in-file `test` blocks through magus's own embedded engine.

**Runtime name:** `buzz` (source `spells/buzz/`)

**Version probe:** none

## Passing arguments to ops

Every op is invoked as `buzz["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key     | Type    | Description                                                                                                                                                                                                                                                                                                                                                            | Source                                                                                              |
| ------- | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| `args`  | `[str]` | Extra arguments appended to the resolved command, replacing any trailing defaults the op declares (go-test's `./...`), so passing args also states the scope. Omit it and a bare `buzz["<op>"]()` keeps the defaults and forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L180) |
| `stdin` | `str`   | Data written to the command's standard input.                                                                                                                                                                                                                                                                                                                          | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L184) |


Working directory and environment are NOT options: they ride the context, as `buzz["<op>"](ctx.withCwd("sub"))` and `buzz["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise; passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

