---
title: "restates"
description: "A house-style rule, off until a decisions table turns it on: it reports a one-line stylesheet comment that only names the rule or declaration it sits on."
tags: [proofread, rules, restates, off]
aliases: [reference/prose/restates]
---

# restates

A house-style rule, off until a decisions table turns it on: it reports a one-line stylesheet comment that only names the rule or declaration it sits on.

## What it catches

A one-line stylesheet comment that only names the rule or declaration it sits on.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: the doc stub rule with the code as the symbol. "Reset the margin" over `margin: 0` tells a maintainer what the line already says and nothing about why it is there.

## Default decision

The code is `PRF6007`. The decision depends on the kind of text judged:

| Kind         | Default |
| ------------ | ------- |
| `stylesheet` | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of plain writing. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"restates": "deny"},
  "paths": {"blog/**": {"restates": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "restates", "code": "PRF6007", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/restates/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
