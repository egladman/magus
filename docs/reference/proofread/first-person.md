---
title: "first-person"
description: "A house-style rule, off until a decisions table turns it on: it reports I, me or my on a reference page or in a guide, outside an author's note."
tags: [proofread, rules, first-person, off]
aliases: [reference/prose/first-person]
---

# first-person

A house-style rule, off until a decisions table turns it on: it reports I, me or my on a reference page or in a guide, outside an author's note.

## What it catches

I, me or my on a reference page or in a guide, outside an author's note.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A page states what the product does, so a reader takes every sentence as its contract. "I use a worktree" in that voice reads as advice the product gives, or as a promise it keeps. An author's note, a blockquote opened with [!AUTHOR], marks the one place a page speaks as its author, and the rule leaves it alone. Quoted words and code are mentions, so a reader's question ("how do I build this?") passes, as do "I/O" and a link's text.

## Default decision

The code is `PRF1005`. The decision depends on the kind of text judged:

| Kind        | Default |
| ----------- | ------- |
| `reference` | off     |
| `guide`     | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of plain writing. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"first-person": "deny"},
  "paths": {"blog/**": {"first-person": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "first-person", "code": "PRF1005", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/first-person/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
