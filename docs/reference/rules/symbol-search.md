---
title: "symbol-search: a text search of the tree for a symbol, a declaration or a diagnostic code the graph answers"
description: "A deny rule: it refuses a text search of the tree for a symbol, a declaration or a diagnostic code the graph answers, and names what to run instead."
tags: [guard, rules, symbol-search, deny]
---

# symbol-search

A deny rule: it refuses a text search of the tree for a symbol, a declaration or a diagnostic code the graph answers, and names what to run instead.

## What it catches

A text search of the tree for a symbol, a declaration or a diagnostic code the graph answers.

## Why

Each alternative of the pattern is classified by what it looks for, and the deny names every classification and why, so a false positive is disputable from the message alone. A name is a declaration lookup (`func X`, `type X`, `class X`, `function X`, `const X`), a CamelCase identifier, or a short name the syntax marks as one: a word search (`\bX\b`, -w), a qualified member (`pkg.X`, `\.X`) or an assignment, when it is capitalized. Text is everything else: a plain word, which is as likely prose; snake_case, which in this tree is a config key, a JSON tag or a Buzz function; a lowercase call or member (`Mkdir(` is os.Mkdir as often as a local function); a standard-library member (`os.Rename`); a declaration of a lowercase word many share (`func main`); a string literal, a path, a hyphenated word or a regular expression. With -i a plain or marked short name widens to text, and a CamelCase one stays a name. The deny fires when ANY alternative is a name the index defines, serving `magus refs X --occurrences` per name (`--definition --source` for a declaration lookup, which prints the body in place of the grep-then-sed pair) and `magus refs --text <literal> <paths>` for each literal text alternative, and `magus explain diagnostic:<code>` for a code the graph holds a node for. A stale index still refuses, serving `magus graph build --silent` first: it still knows the names it held, and it vouches for a declaration lookup or a CamelCase call it has not indexed yet, since that is the name a branch is adding; a bare word it does not hold stays text. Measured 2026-09-30: the fail-open advice a stale index used to give let every symbol search through, since an index goes stale on the first edit. Only a workspace with no symbol index at all is advised instead, since a deny there routes nowhere. An index built at another revision is stale on the same terms; a rebase still underway turns the deny into graph-stale's advice, since a rebuild then would describe a tree about to move. The search must reach the tree: a directory, a glob, or several files. One named file is a read and runs, with refs advised when the index vouches for the name; a definition lookup carrying -A, -B or -C is a read of the body, and grep-reader refuses it. The files refs answers for are the languages a spell declares a symbol indexer for (here Go and TypeScript), read from the spell catalog. A search of stdin, Markdown, a log, Buzz (source no indexer reads, so a Buzz `fun` or `object` lookup is text too), a directory holding none of those languages (a skills tree, fixtures), a dot-directory (.github, .git), node_modules, a revision, or a tree outside the workspace runs. A search of the tree carries the index's own answer when the index is current: every file under the searched paths with its occurrence count and lines, as `magus refs` prints them. It is refs' answer, not grep's: comments, strings and prose are not in it. A pipe after the search is not reproduced. The deny still carries the unfiltered answer and says so: a model of sort, sed or awk substituted for the real tool diverges from it. Measured 2026-09-24 over 14,773 search patterns: 45% were alternations and 13% definition lookups. Measured 2026-09-30: of the 1,211 distinct search lines the guard recorded that week, the old rule refused one.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [symbol-search]: ...
```

`magus describe rule symbol-search` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
