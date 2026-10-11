---
title: "blame"
description: "A rule that denies on `change-description`, `review-reply`, `commit-message`, and `issue` and advises on `agent-reply` by default: it reports a person or past work as the subject of a fault, and contempt for code or a decision."
tags: [proofread, rules, blame, deny]
aliases: [reference/prose/blame]
---

# blame

A rule that denies on `change-description`, `review-reply`, `commit-message`, and `issue` and advises on `agent-reply` by default: it reports a person or past work as the subject of a fault, and contempt for code or a decision.

## What it catches

A person or past work as the subject of a fault, and contempt for code or a decision.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Text loses its tone on the way to a reader, who fills the gap with intent the writer never had. "Whoever wrote this forgot to" reads as an accusation; "the rename left the old key" states the same fact. The first person is left alone, since owning a fault reads as candor. Measured 2026-10-10 before a fix: 85.7% precision on its cases (6 of 7), firing on 0.1% of human AIDev review comments and on 1 magus commit body; of 70 firings read, 30 were wrong. So "should have" counts only before a past participle ("you should have a tests file" is possession, "they should have been kept" a thing), "they" only when it forgot or neglected to, and a contempt word is exempt inside a name ("crazy-max", "dumb-init"), capitalized mid-sentence ("Keep It Simple, Stupid"), as "go too crazy", and as "lazy loading". After: 100% on its cases; firing rates measured at merge.

## Default decision

The code is `PRF2001`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | deny    |
| `review-reply`       | deny    |
| `commit-message`     | deny    |
| `issue`              | deny    |
| `agent-reply`        | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"blame": "advise"},
  "paths": {"blog/**": {"blame": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "blame", "code": "PRF2001", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/blame/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
