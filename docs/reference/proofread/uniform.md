---
title: "uniform"
description: "A house-style rule, off until a decisions table turns it on: it reports a text more even than its author writes: list items of one length, items on one opening word, or sentences that all open on one part of speech, past the author's own 5th percentile on two or more of them."
tags: [proofread, rules, uniform, off]
aliases: [reference/prose/uniform]
---

# uniform

A house-style rule, off until a decisions table turns it on: it reports a text more even than its author writes: list items of one length, items on one opening word, or sentences that all open on one part of speech, past the author's own 5th percentile on two or more of them.

## What it catches

A text more even than its author writes: list items of one length, items on one opening word, or sentences that all open on one part of speech, past the author's own 5th percentile on two or more of them.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A text cleared of every finding comes out evenly shaped, and that evenness is itself a mark of machine-written text. Runs only with a voice file, whose windows hold the author's own range of each measure; a text is held to that range, never to a shared one, and nothing is added to make a text uneven. A paired calibration of one author's texts against rewrites that cleared every finding found the rewrites narrowed what sentences open on, but evened out neither sentence nor paragraph length, so those are not read; it had too few lists to measure the item spreads. The author's ranges come from 200-word windows, so a text under 200 words is not judged alone; `proofread --set` pools short texts, such as a branch's commits, into one. No firing has been labeled, so it ships off.

## Default decision

The code is `PRF1041`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | off     |
| `review-summary`     | off     |
| `review-inline`      | off     |
| `review-reply`       | off     |
| `commit-message`     | off     |
| `issue`              | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of plain writing. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"uniform": "deny"},
  "paths": {"blog/**": {"uniform": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "uniform", "code": "PRF1041", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/uniform/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
