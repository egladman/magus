---
title: "ambiguous-ci-word: a call that picks magus `ci` or the host's CI/CD while the person's bare word \"ci\" is still unresolved"
description: "A deny rule by default: it refuses a call that picks magus `ci` or the host's CI/CD while the person's bare word \"ci\" is still unresolved, and names what to run instead."
tags: [guard, rules, ambiguous-ci-word, deny]
---

# ambiguous-ci-word

A deny rule by default: it refuses a call that picks magus `ci` or the host's CI/CD while the person's bare word "ci" is still unresolved, and names what to run instead.

## What it catches

A call that picks magus `ci` or the host's CI/CD while the person's bare word "ci" is still unresolved.

## Why

The bare word "ci" names two different things in a magus workspace: the reserved `ci` target (often reached as `magus affected ci`), and the host's CI/CD run (GitHub Actions, GitLab CI, and the rest). An agent that assumes either burns a turn on the wrong gate, then another clarifying with the person. A message the person submits with bare "ci" and no disambiguator marks the session; the next call that runs `magus run ci` / `magus affected ci`, a host CI CLI (`gh run`, `act`, ...), or touches a workflow path is refused until they say which. A clarifying message ("affected ci", "github actions", "the gate", "CI/CD") resolves the mark and later calls pass. It is not a skill-load rule: the remedy is to ask, not to read a skill first.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"ambiguous-ci-word": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [ambiguous-ci-word]: ...
```

A workspace that sets this rule to `advise` sees `advise [ambiguous-ci-word]: ...` instead.

`magus describe rule ambiguous-ci-word` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
