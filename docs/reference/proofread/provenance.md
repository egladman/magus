---
title: "provenance"
description: "A deny rule by default: it refuses a date, a pull request or issue number, a commit hash or an incident in a code comment."
tags: [proofread, rules, provenance, deny]
aliases: [reference/prose/provenance]
---

# provenance

A deny rule by default: it refuses a date, a pull request or issue number, a commit hash or an incident in a code comment.

## What it catches

A date, a pull request or issue number, a commit hash or an incident in a code comment.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: where the code came from is the log's to say. A date or a number ages the moment it is written; a measurement of what the code does stays.

## Default decision

The code is `PRF5012`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `code-comment` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"provenance": "advise"},
  "paths": {"blog/**": {"provenance": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "provenance", "code": "PRF5012", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/provenance/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
