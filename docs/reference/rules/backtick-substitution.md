---
title: "backtick-substitution: a backtick command substitution, which inside double quotes runs a command"
description: "A deny rule by default: it refuses a backtick command substitution, which inside double quotes runs a command, and names what to run instead."
tags: [guard, rules, backtick-substitution, deny]
---

# backtick-substitution

A deny rule by default: it refuses a backtick command substitution, which inside double quotes runs a command, and names what to run instead.

## What it catches

A backtick command substitution, which inside double quotes runs a command.

## Why

Inside double quotes a backtick starts a command substitution, so a pattern or a message carrying a literal backtick runs code: the backtick pairs with the next one anywhere on the line, and everything between them becomes one command. A `grep` whose pattern and a later stage's pattern each hold a triple backtick pair them into ONE substitution that swallows the file operand and the rest of the pipeline. What is left is a `grep` with no file, reading a stdin that never closes. A literal backtick belongs in single quotes, where it is text. A substitution is written `$(...)`, which nests and cannot pair with a stray backtick. Backticks inside single quotes and inside a quoted heredoc (`<<'EOF'`) are text and never fire; neither does a line that does not parse.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"backtick-substitution": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [backtick-substitution]: ...
```

`magus describe rule backtick-substitution` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
