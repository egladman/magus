---
title: "voice-drift"
description: "An advisory by default: it explains, and blocks nothing, on a draft that reads further from its author's own texts of its kind than 95 percent of them do, or a commit subject longer than 95 percent of the author's."
tags: [proofread, rules, voice-drift, advise]
aliases: [reference/prose/voice-drift]
---

# voice-drift

An advisory by default: it explains, and blocks nothing, on a draft that reads further from its author's own texts of its kind than 95 percent of them do, or a commit subject longer than 95 percent of the author's.

## What it catches

A draft that reads further from its author's own texts of its kind than 95 percent of them do, or a commit subject longer than 95 percent of the author's.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Runs only with a voice file, which proofread voice build measures from the author's own texts, 1,000 words or more of a kind, shuffled and pooled into windows of 200 words or more. A draft of 200 words or more is ranked on commas, parentheses, colons, semicolons, "I", "and", repeated part-of-speech 4-grams, part-of-speech compression and participial clauses, each only in the direction agent drafts move, and the ranks combine by Fisher's method. The draft is scored on the features it has data for, against the author's windows with data for all of them, and the draft and each of those windows are scored alike against the rest; the draft drifts when it scores past all but 5 percent of the windows. A commit message is held to the 95th percentile of the author's subject lengths instead. The features and their directions come from voice-calibration, which measured one author's held-out text against agent drafts and against light agent edits of that text: the rule separates drafts, not edits. No firing has been labeled, so the rule advises.

## Default decision

The code is `PRF1040`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | advise  |
| `review-reply`       | advise  |
| `commit-message`     | advise  |
| `issue`              | advise  |
| `review-summary`     | advise  |
| `review-inline`      | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"voice-drift": "deny"},
  "paths": {"blog/**": {"voice-drift": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "voice-drift", "code": "PRF1040", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/voice-drift/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
