---
title: "suppression-unused"
description: "A deny rule by default: it refuses a suppression comment that gives no reason or that matched no finding."
tags: [proofread, rules, suppression-unused, deny]
aliases: [reference/prose/suppression-unused]
---

# suppression-unused

A deny rule by default: it refuses a suppression comment that gives no reason or that matched no finding.

## What it catches

A suppression comment that gives no reason or that matched no finding.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A suppression is a claim that a finding is wrong here, and a reviewer can only weigh the claim when the reason is written beside it. One with no reason suppresses nothing. One that matched nothing is left over from text that has since changed, and it would hide the next finding that lands on its lines, so it is reported for removal.

## Default decision

The code is `PRF1090`. The decision depends on the kind of text judged:

| Kind                          | Default |
| ----------------------------- | ------- |
| `doc-comment`                 | deny    |
| `reference`                   | deny    |
| `change-description`          | deny    |
| `agent-instructions`          | deny    |
| `agent-instructions-template` | deny    |
| `guide`                       | deny    |
| `review-reply`                | deny    |
| `message`                     | deny    |
| `commit-message`              | deny    |
| `cli-help`                    | deny    |
| `issue`                       | deny    |
| `release-notes`               | deny    |
| `changelog`                   | deny    |
| `agent-reply`                 | deny    |
| `tool-description`            | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"suppression-unused": "advise"},
  "paths": {"blog/**": {"suppression-unused": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "suppression-unused", "code": "PRF1090", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/suppression-unused/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
