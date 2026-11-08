---
title: "bare-symbol"
description: "A house-style rule, off until a decisions table turns it on: it reports a code name outside backticks: a call, a dotted name, a lowerCamel or a snake_case word."
tags: [proofread, rules, bare-symbol, off]
aliases: [reference/prose/bare-symbol]
---

# bare-symbol

A house-style rule, off until a decisions table turns it on: it reports a code name outside backticks: a call, a dotted name, a lowerCamel or a snake_case word.

## What it catches

A code name outside backticks: a call, a dotted name, a lowerCamel or a snake_case word.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: a name in backticks is one a reader can search for, and prose around it stays prose. Product names that look like code (macOS, iPhone, Node.js) are left alone, and so is PascalCase, which is how most brands are spelled.

## Default decision

The code is `PRF5011`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | off     |
| `review-summary`     | off     |
| `review-inline`      | off     |
| `review-reply`       | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of plain writing. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"bare-symbol": "deny"},
  "paths": {"blog/**": {"bare-symbol": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "bare-symbol", "code": "PRF5011", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/bare-symbol/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
