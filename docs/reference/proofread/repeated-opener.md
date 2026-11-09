---
title: "repeated-opener"
description: "A house-style rule, off until a decisions table turns it on: it reports a bulleted list of 5 or more items where one opening word starts 70% of them."
tags: [proofread, rules, repeated-opener, off]
aliases: [reference/prose/repeated-opener]
---

# repeated-opener

A house-style rule, off until a decisions table turns it on: it reports a bulleted list of 5 or more items where one opening word starts 70% of them.

## What it catches

A bulleted list of 5 or more items where one opening word starts 70% of them.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A list where most items open on one word ("Wrote" on 9 of 12 bullets) makes the reader skim past the word to find what each item says, and reads as a template. list-parallel asks the items to share a shape; this asks them not to share a word, so a list of different past-tense verbs passes both, and one verb on every item passes list-parallel and is reported here. uniform, with a voice, also judges the items' lengths. The threshold is where the hand-labeled lists in its case file separate: every hit holds 4 of 5 or more, every pass at most 3 of 5 or 4 of 6, and no list under 5 items is a hit. A numbered list, an article opener (a, an, the) and an item that is only code or a link are not judged. The cases are invented and no labeling round has read a firing, so it ships off.

## Default decision

The code is `PRF1062`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
| `principle`          | off     |
| `change-description` | off     |
| `agent-brief`        | off     |
| `guide`              | off     |
| `review-summary`     | off     |
| `review-inline`      | off     |
| `review-reply`       | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |
| `agent-reply`        | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of plain writing. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"repeated-opener": "deny"},
  "paths": {"blog/**": {"repeated-opener": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "repeated-opener", "code": "PRF1062", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/repeated-opener/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
