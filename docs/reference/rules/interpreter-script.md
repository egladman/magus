---
title: "interpreter-script: an ad-hoc script in python, node, perl or ruby, where Buzz is the workspace's scripting language"
description: "A deny rule by default: it refuses an ad-hoc script in python, node, perl or ruby, where Buzz is the workspace's scripting language, and names what to run instead."
tags: [guard, rules, interpreter-script, deny]
---

# interpreter-script

A deny rule by default: it refuses an ad-hoc script in python, node, perl or ruby, where Buzz is the workspace's scripting language, and names what to run instead.

## What it catches

An ad-hoc script in python, node, perl or ruby, where Buzz is the workspace's scripting language.

## Why

Buzz is the one scripting language here: `magus buzz -e '<buzz>'` runs a program with fs, json and proc built in, the magus-buzz-lang skill carries its syntax, and the guard reads what the program does. A python or perl one-liner arrives as one string the guard cannot follow, and its commonest shape, a substitute-and-write, cannot tell your identifier from a dependency's of the same name. It denies python, python3 and python3.x given -c, a `-` operand, no program at all (a heredoc or piped stdin), or a .py file, and a .py file run directly; node given -e, -p, --eval, --print, `-` or no program; perl with any flag cluster carrying e, E or i, or reading its program from stdin; ruby given -e or -i, or reading stdin. Untouched: --version, -V and --help, `python -m`, `node file.js`, `perl file.pl`, bash and sh, `magus run` and `magus buzz`. A script file is judged by its program too: a node, perl or ruby file that substitutes and writes back a path in the workspace, and a write that leaves one, get this verdict; a program whose every named path lies outside the workspace is untouched. A Buzz program is held to the line it would be typed as: every proc\exec and proc\shell a `magus buzz -e` body, stdin heredoc or `magus buzz <script>` file starts, spelled from literals, is judged as that command, so `proc\exec("go", args: ["test", "./..."])` meets raw-tool the way `go test ./...` does. That is the guard reading a hooked agent call, not a limit on Buzz: the proc module and the interpreter run anything, and a script run outside the harness is never judged.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"interpreter-script": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [interpreter-script]: ...
```

A workspace that sets this rule to `advise` sees `advise [interpreter-script]: ...` instead.

`magus describe rule interpreter-script` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
