---
title: "inline-alias: a VCS alias defined inline (`git -c alias.x=...`), which hides the command it runs"
description: "A deny rule by default: it refuses a VCS alias defined inline (`git -c alias.x=...`), which hides the command it runs, and names what to run instead."
tags: [guard, rules, inline-alias, deny]
---

# inline-alias

A deny rule by default: it refuses a VCS alias defined inline (`git -c alias.x=...`), which hides the command it runs, and names what to run instead.

## What it catches

A VCS alias defined inline (`git -c alias.x=...`), which hides the command it runs.

## Why

git expands `git -c alias.x='reset --hard' x` into `git reset --hard`, so the word every git rule reads as the subcommand names nothing, and a reset, clean or push would pass unjudged. The guard refuses the line rather than judging it as every destructive verb at once: the arguments those rules read come from the alias body too, and `--config-env` or an inline `include.path`, which loads a file that may define aliases, keeps the body off the line altogether. The other backends are held to the same bar: hg's and sl's `--config alias.x=...`, jj's `--config aliases.x=...`, and the options that load config from a file or a TOML string (`--config-file`, sl's `--configfile`, jj's `--config-toml`). Spell out the command the alias stands for. Any other config setting is untouched, and each tool's global options (`git -C`, `hg -R`, `jj --at-op`, `--no-pager`) are read past the way the tool reads them.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"inline-alias": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

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
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
