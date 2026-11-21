---
title: "next-housekeeping"
description: "A deny rule by default: it refuses a follow-up section that asks someone to check CI, obtain reviews or merge the current pull request."
tags: [proofread, rules, next-housekeeping, deny]
aliases: [reference/prose/next-housekeeping]
---

# next-housekeeping

A deny rule by default: it refuses a follow-up section that asks someone to check CI, obtain reviews or merge the current pull request.

## What it catches

A follow-up section that asks someone to check CI, obtain reviews or merge the current pull request.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Next describes substantive work after the change, such as extending the API or replacing a consumer. Checking CI, obtaining approval and merging are administration of the current pull request. The rule matches an administrative action and its review or validation target within a follow-up section. Adding CI coverage, changing a review system and discussing architecture remain substantive work. This is a change-description section contract, not a measured style score.

## Default decision

The code is `PRF1006`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"next-housekeeping": "advise"},
  "paths": {"blog/**": {"next-housekeeping": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "next-housekeeping", "code": "PRF1006", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/next-housekeeping/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
