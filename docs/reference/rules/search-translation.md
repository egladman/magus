---
title: "search-translation: a text search whose pattern a graph query provably answers with the same entities"
description: "A deny rule: it refuses a text search whose pattern a graph query provably answers with the same entities, and names what to run instead."
tags: [guard, rules, search-translation, deny]
---

# search-translation

A deny rule: it refuses a text search whose pattern a graph query provably answers with the same entities, and names what to run instead.

## What it catches

A text search whose pattern a graph query provably answers with the same entities.

## Why

The pattern is compiled in the tool's own dialect (BRE, ERE or fixed) and run against the graph's ids when the command is judged, so the deny names a query that was checked rather than one that looks equivalent. Three shapes qualify. A pattern that can only match MGS codes (`MGS30[23]`, `MGS30..`, `MGS302[0-9]\|MGS303[0-9]`), over any path in the workspace, becomes `magus query kind=diagnostic 'id=~^diagnostic:...$'`, and a single literal code keeps symbol-search's `magus explain diagnostic:<code>`. A pattern selecting every Markdown heading of the files searched (`^#`, `^#\+`), when those lines match the section nodes the graph holds file for file and none sits in a code fence, becomes `magus query kind=docsection 'id=~^docsection:<file>#'`. A search of a magusfile whose every hit declares a target the graph holds becomes `magus explain target:<project>:<name>`. Anything else stays silent: -i, -v, -c, -l, -x, context flags, a level-specific heading pattern, a BZZ code, a line anchor on a code, a heading inside a fence, one hit that is a call or a comment, stdin, or a tree outside the workspace. Measured 2026-09-24: 14,773 searches, 45% of them alternations, and graph verbs used about 50 times less than grep.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [search-translation]: ...
```

`magus describe rule search-translation` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
