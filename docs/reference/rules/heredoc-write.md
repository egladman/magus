---
title: "heredoc-write: a here-document written to a file, judged as the file write it is"
description: "A deny rule by default: it refuses a here-document written to a file, judged as the file write it is, and names what to run instead."
tags: [guard, rules, heredoc-write, deny]
---

# heredoc-write

A deny rule by default: it refuses a here-document written to a file, judged as the file write it is, and names what to run instead.

## What it catches

A here-document written to a file, judged as the file write it is.

## Why

`cat > f.go <<'EOF'` and `tee f.go <<'EOF'` write a file the way a host's write tool does, so the body is judged as that write: the comment, lease and workspace write rules hold on either channel. A refused write refuses the line under this name, with the write rule's own in brackets, and advice about the write rides the line. Only a literal body is judged: one that expands a parameter or runs a substitution is text nobody can read before it runs. A target in /dev, a stderr redirect and a heredoc feeding a command's stdin are not writes. It cannot tell a scratch file the same line deletes from a file meant to stay.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"heredoc-write": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [heredoc-write]: ...
advise [heredoc-write]: ...
```

`magus describe rule heredoc-write` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
