---
title: "filler"
description: "A deny rule by default: it refuses throat-clearing (\"Note that\") and filler adverbs (\"simply\", \"basically\")."
tags: [proofread, rules, filler, advise]
aliases: [reference/prose/filler]
---

# filler

A deny rule by default: it refuses throat-clearing ("Note that") and filler adverbs ("simply", "basically").

## What it catches

Throat-clearing ("Note that") and filler adverbs ("simply", "basically").

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

The words carry nothing the sentence needs. Written text takes a wider list ("actually", "robust") than doc comments, which keep the narrow one until a sweep clears the wider; the senses that carry meaning ("just" as merely, "very" as the same one) are exempt. Measured 2026-10-10 before a fix: 70.6% precision on its cases (12 of 17), firing on 21.8% of AIDev pull requests, 4.7% of human review comments (6.7% of their replies), 3.8% of AIDev commits and 5.5% of magus commit bodies; 18 of 45 firings read in human replies were wrong. Lowercase "actually" stated a contrast ("what the cache actually did", "wait for it to actually exit") in 10 of 11 human replies and about 22 of 25 agent pull request bodies, so only the opener "Actually," counts. Also exempt: a word hyphenated into a compound ("all-powerful"), "very" grading a size or position ("a very long path"), "just" after a negation other than a copula ("don't just"), before a literal or as recency ("Just pushed"), and "This function" outside a doc comment, where it points at code. Measured 2026-10-11 over the adjudicated labeling round: 86.2% precision (50 of 58) before, 100% (49 of 49) after on the same cases, which the exceptions were tuned on. Also exempt now: lowercase "really" meaning in fact ("should really bind"), "Just" before a determiner or "not", a selling word in a comparison ("a more comprehensive check"; this loses "a more powerful parser"), and a word mentioned after "the filler". On fresh text it held 32 of 40 (lower bound 0.65, validation round 3), under the advise gate, so it ships off until it clears 0.70.

## Default decision

The code is `PRF4001`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `doc-comment`        | off     |
| `reference`          | off     |
| `guide`              | off     |
| `change-description` | off     |
| `agent-instructions` | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |
| `cli-help`           | off     |
| `review-reply`       | off     |
| `agent-reply`        | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"filler": "deny"},
  "paths": {"blog/**": {"filler": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "filler", "code": "PRF4001", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/filler/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
