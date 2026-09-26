---
title: "inline-alias: a git line defining an alias inline (`-c alias.x=...`), which hides the command it runs"
description: "A deny rule: it refuses a git line defining an alias inline (`-c alias.x=...`), which hides the command it runs, and names what to run instead."
tags: [guard, rules, inline-alias, deny]
---

# inline-alias

A deny rule: it refuses a git line defining an alias inline (`-c alias.x=...`), which hides the command it runs, and names what to run instead.

## What it catches

A git line defining an alias inline (`-c alias.x=...`), which hides the command it runs.

## Why

git expands `git -c alias.x='reset --hard' x` into `git reset --hard`, so the word every git rule reads as the subcommand names nothing, and a reset, clean or push would pass unjudged. The guard refuses the line rather than judging it as every destructive verb at once: the arguments those rules read come from the alias body too, and `--config-env` or an inline `include.path`, which loads a file that may define aliases, keeps the body off the line altogether. Spell out the command the alias stands for. Any other `-c` setting is untouched, and the global options before a subcommand (`-C`, `--no-pager`, `--git-dir`) are read past the way git reads them.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [inline-alias]: ...
```

`magus describe rule inline-alias` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
