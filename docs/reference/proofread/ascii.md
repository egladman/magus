---
title: "ascii"
description: "A house-style rule, off until a decisions table turns it on: it reports a curly quote, an ellipsis character or an emoji in prose."
tags: [proofread, rules, ascii, off]
aliases: [reference/prose/ascii]
---

# ascii

A house-style rule, off until a decisions table turns it on: it reports a curly quote, an ellipsis character or an emoji in prose.

## What it catches

A curly quote, an ellipsis character or an emoji in prose.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: curly quotes prove nothing about who wrote a text; the rule encodes one repository's ASCII-only policy. Arrows and box drawing stay legal, having no plain spelling.

## Default decision

The code is `PRF5003`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
| `guide`              | off     |
| `change-description` | off     |
| `agent-instructions` | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |
| `review-reply`       | off     |
| `agent-reply`        | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"ascii": "deny"},
  "paths": {"blog/**": {"ascii": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "ascii", "code": "PRF5003", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/ascii/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
