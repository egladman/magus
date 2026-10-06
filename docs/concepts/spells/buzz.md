---
title: buzz spell
generated_from: spells/buzz/spell.buzz
description: "Buzz language identity and symbol index: the .buzz extension, the comment and string syntax magus reads source with, and scip-buzz."
tags: [buzz, spell, language, tools]
---

# buzz

The `buzz` spell declares what a Buzz file IS and how to index it. It is what lets the knowledge graph tell code from a comment or a string in a `.buzz` source, and what binds the extension to its project. Its one op, `scip-buzz`, builds the project's Buzz symbol index beside any other index the project has, so `magus refs` and renames reach Buzz. Running Buzz needs no spell: `magus buzz -t <file>` executes a file's in-file `test` blocks through magus's own embedded engine.

**Runtime name:** `buzz` (source `spells/buzz/`)

**Version probe:** none

## Passing arguments to ops

Every op is invoked as `buzz["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key     | Type    | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | Source                                                                                              |
| ------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------- |
| `args`  | `[str]` | Extra arguments appended to the resolved command, replacing any trailing defaults the op declares (go-test's `./...`), so passing args also states the scope. Omit it and a bare `buzz["<op>"]()` keeps the defaults and forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. To keep the passthrough too, append the target's own `args` parameter: `{"args": ["-race", "./..."] + args}`. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L190) |
| `stdin` | `str`   | Data written to the command's standard input.                                                                                                                                                                                                                                                                                                                                                                                                                                        | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L194) |


Working directory and environment are NOT options: they ride the context, as `buzz["<op>"](ctx.withCwd("sub"))` and `buzz["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise; passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## scip-buzz

Indexes the project's Buzz into the cache: magus injects MAGUS_SYMBOL_INDEX with the destination and runs the op from the project dir, which scip-buzz indexes. A project bound to another indexing spell too (go, typescript) keeps that index under `scip` and builds this one beside it as `scip-buzz`, so a missing scip-buzz never fails the other index.

**Command:** `scip-buzz --output $MAGUS_SYMBOL_INDEX`

