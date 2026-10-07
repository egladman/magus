---
title: "search-translation: a text search whose pattern a graph query provably answers with the same entities"
description: "An advisory by default: it explains, and blocks nothing, on a text search whose pattern a graph query provably answers with the same entities."
tags: [guard, rules, search-translation, advise]
---

# search-translation

An advisory by default: it explains, and blocks nothing, on a text search whose pattern a graph query provably answers with the same entities.

## What it catches

A text search whose pattern a graph query provably answers with the same entities.

## Why

The pattern is compiled in the tool's own dialect (BRE, ERE or fixed) and run against the graph's ids when the command is judged, so the deny names a query that was checked rather than one that looks equivalent, and carries that query's answer, bounded to twenty results and a count, so the refused search costs nothing. These shapes qualify. A pattern that can only match MGS codes (`MGS30[23]`, `MGS30..`, `MGS302[0-9]\|MGS303[0-9]`), over any path in the workspace, becomes `magus query kind=diagnostic 'id=~^diagnostic:...$'`, and a single literal code keeps symbol-search's `magus explain diagnostic:<code>`. A pattern selecting every Markdown heading of the files searched (`^#`, `^#\+`), when those lines match the section nodes the graph holds file for file and none sits in a code fence, becomes `magus query kind=docsection 'id=~^docsection:<file>#'`. A search of a magusfile whose every hit declares a target the graph holds becomes `magus explain target:<project>:<name>`. A search of one Go file whose every hit declares a symbol the index holds (`^func`, `^func Test`, `func (s \*Store)`) becomes `magus explain file:<path>`, with the names and their lines inline. A file-finding call whose every file is a node the graph holds becomes `magus query kind=file 'id=~^file:...'`: a pattern when one selects exactly those files, else the files enumerated when there are twenty or fewer. The proof walks what the call walks, so it holds whatever revision the index was built at. It covers `find` (-name, -path, their negations, -type f, -maxdepth), `fd` (a name pattern or glob, -e, -t f, -d, smart case), `rg --files` (-g and -t), `ls -R <dir>`, and an `ls <dir>` whose every visible entry is a file node or a directory holding one, which becomes `magus query 'id=~^(?:file|dir):<dir>/[^/]+$'`. `git ls-files [<dir|glob>]`, alone or piped into a search of its paths, is proved against what version control tracks: the graph indexes only some tracked files and never an untracked one, so the deny answers for the indexed ones and names every other match; past twenty such files it is silent. A metadata flag (-l, -a, -t, -S), hidden or ignored files (fd -H, rg -uu), an untracked-files question (`--others`), one named file (a tracked check), an inverted or counted filter, a file the graph does not index, or a walk past the budget is silent. A host's own content and file search tools are judged as the rg and find lines they stand for, read by the shape of their input. A pipe after the search is not reproduced, except the search a tracked listing is piped into: the deny carries the query's unfiltered answer and says so, rather than a model of the filter that could diverge from the real tool. Anything else stays silent: -i, -v, -c, -l, -x, context flags, a stale index, a level-specific heading pattern, a BZZ code, a line anchor on a code, a heading inside a fence, one hit that is a call or a comment, stdin, or a tree outside the workspace. A graph describing another tree advises, as graph-stale, except for a listing, which the walk proves. 

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"search-translation": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [search-translation]: ...
```

`magus describe rule search-translation` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
