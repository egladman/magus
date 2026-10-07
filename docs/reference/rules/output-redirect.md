---
title: "output-redirect: magus output sent to a file or discarded, which the run log already holds"
description: "An advisory by default: it explains, and blocks nothing, on magus output sent to a file or discarded, which the run log already holds."
tags: [guard, rules, output-redirect, advise]
---

# output-redirect

An advisory by default: it explains, and blocks nothing, on magus output sent to a file or discarded, which the run log already holds.

## What it catches

Magus output sent to a file or discarded, which the run log already holds.

## Why

Silencing and keeping are the only two intents and magus has a lever for each: `--silent` says nothing until something fails, and `-o json --tee <file>` keeps the STRUCTURED output rather than console text, which is not a format anything should parse. A target run persists its whole log either way and prints a ref for it, so capturing the console is redundant. It judges where each stream ENDS. Either stream landing in a file fires, and so does stdout landing in /dev/null. `2>/dev/null` fires on `run`, `affected` and `x`, which write their failure block (cause, output ref, reproduce line) to stderr, and passes on every other verb, whose stderr carries at most an error line the exit status also reports. `2>&1` alone passes: both streams still reach the reader.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"output-redirect": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [output-redirect]: ...
```

`magus describe rule output-redirect` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
