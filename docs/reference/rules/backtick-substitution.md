---
title: "backtick-substitution: a backtick command substitution, which inside double quotes runs a command"
description: "A deny rule: it refuses a backtick command substitution, which inside double quotes runs a command, and names what to run instead."
tags: [guard, rules, backtick-substitution, deny]
---

# backtick-substitution

A deny rule: it refuses a backtick command substitution, which inside double quotes runs a command, and names what to run instead.

## What it catches

A backtick command substitution, which inside double quotes runs a command.

## Why

Inside double quotes a backtick starts a command substitution, so a pattern or a message carrying a literal backtick runs code: the backtick pairs with the next one anywhere on the line, and everything between them becomes one command. Measured here: a `grep` whose pattern and a later stage's pattern each held a triple backtick paired them into ONE substitution that swallowed the file operand and the rest of the pipeline. What was left was a `grep` with no file, reading a stdin that never closed, and it held a subagent for two hours. A literal backtick belongs in single quotes, where it is text. A substitution is written `$(...)`, which nests and cannot pair with a stray backtick. Backticks inside single quotes and inside a quoted heredoc (`<<'EOF'`) are text and never fire; neither does a line that does not parse.

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
