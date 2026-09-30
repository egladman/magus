---
title: "stdin-closed: shell commands run with stdin at end-of-file, said once per session"
description: "An advisory: it explains, and blocks nothing, on shell commands run with stdin at end-of-file, said once per session."
tags: [guard, rules, stdin-closed, advise]
---

# stdin-closed

An advisory: it explains, and blocks nothing, on shell commands run with stdin at end-of-file, said once per session.

## What it catches

Shell commands run with stdin at end-of-file, said once per session.

## Why

An agent's shell command inherits an open stdin nobody writes to, so anything that reads it (grep or cat with no operand, read, a prompt, ssh, a pager) waits forever, and a host that times the call out backgrounds it rather than killing it. Measured 2026-09-29: a grep whose file operands expanded to nothing read that stdin for 3.5 hours. Where the host lets a hook rewrite the call, the guard prefixes the command with `exec </dev/null;`, never on a refused call and never twice. A heredoc, a pipe or a `<` still give a command its input, since each sets stdin for its own command. A prefix rather than a `{ <command>` ... `} </dev/null` group: Claude Code's isolation check for worktree agents judges the rewritten line, and measured 2026-09-29 it refused the group as too complex even around `stat` or `git status`, where it refuses the prefix only on a line it already found borderline (runtime-computed values beside a redirect).

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [stdin-closed]: ...
```

`magus describe rule stdin-closed` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
