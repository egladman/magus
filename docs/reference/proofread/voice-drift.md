---
title: "voice-drift"
description: "An advisory by default: it explains, and blocks nothing, on a text whose style measures outside its author's own range on two or more features of a voice file."
tags: [proofread, rules, voice-drift, advise]
aliases: [reference/prose/voice-drift]
---

# voice-drift

An advisory by default: it explains, and blocks nothing, on a text whose style measures outside its author's own range on two or more features of a voice file.

## What it catches

A text whose style measures outside its author's own range on two or more features of a voice file.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Runs only with a voice file, which proofread voice build measures from the author's own texts. A feature drifts when the text measures under its p10 or over its p90 for the kind, so about one text in five drifts on any one feature by construction, and a finding needs two. A study of one author, 2026-10-10, set p90 thresholds on 254 hand-typed descriptions, 286 replies and 1,055 commits: two or more features over range fired on 9 percent of that author's descriptions against 66 percent of agent descriptions, 7 against 46 percent of replies, and 5 against 60 percent of commits. Those rates are in-sample and count p90 alone, and no firing has been labeled, so the rule advises.

## Default decision

The code is `PRF1040`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | advise  |
| `review-reply`       | advise  |
| `commit-message`     | advise  |
| `issue`              | advise  |

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
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
