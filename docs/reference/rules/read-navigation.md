---
title: "read-navigation: a whole read of a Go, Buzz or Markdown file over 120 lines"
description: "A deny rule: it refuses a whole read of a Go, Buzz or Markdown file over 120 lines, and names what to run instead."
tags: [guard, rules, read-navigation, deny]
---

# read-navigation

A deny rule: it refuses a whole read of a Go, Buzz or Markdown file over 120 lines, and names what to run instead.

## What it catches

A whole read of a Go, Buzz or Markdown file over 120 lines.

## Why

The deny carries the file's declarations or headings with their lines, and the command that prints one of them, so the refused read costs nothing. The map is parsed from the file itself, so a stale index still gets one: where the index vouches for every name, `magus refs <name> --definition --source` prints one checked against it and the graph lists the map; where it does not, and for Buzz, which nothing indexes for refs, `sed -n <first>,<last>p <file>` prints one by its lines. A Go declaration spans its doc comment to its closing brace, each member of a grouped var, const or type is its own entry, and a method is named `Type.Method`. A Buzz entry is a top-level fun, test, object or enum, from its doc comment to the line before the next statement. Every file a `cat` or `nl` prints is judged, so `cat a.go b.go` is refused for whichever mapped file is over the threshold. A host's read tool is restated as the same line (`cat <file>`, or `sed -n` over its offset and limit, a limit over 300 counting as whole) and judged the same way. 120 lines is the p90 of a bounded read; measured 2026-09-26 over 66,548 Bash reads, 8,394 dumped a whole Go, Buzz or Markdown file, and 3.6% of whole reads were followed by an edit of that file. Measured 2026-09-29 over the audit's transcripts: 657 whole reads of Go or Markdown files over 120 lines ran, 3 denied, most of them on a stale index or a multi-file cat (63); about 90% were reads a map serves, the rest a read before an edit (~38), the reader's own new file (2) and a skill read whole (~17). 299 of them came through the host read tool, which nothing judged. Buzz: 125 whole reads over 120 lines, 5 denied by any rule; a hand-read sample of 35 held 29 (83%), the misses a read right before an edit and a review of the reader's own diff. Silent on a short file, TypeScript (no parser here), SKILL.md, AGENTS.md and a host's own instruction file (written to be read whole), a generated output, a path outside the workspace, a file that does not parse, and a read feeding a pipe or redirect.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [read-navigation]: ...
```

`magus describe rule read-navigation` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
