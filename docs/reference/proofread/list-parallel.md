---
title: "list-parallel"
description: "A rule that is off by default: it reports a list item that opens with a verb where the others open with a noun phrase, or in another tense once a decisions table turns it on."
tags: [proofread, rules, list-parallel, off]
aliases: [reference/prose/list-parallel]
---

# list-parallel

A rule that is off by default: it reports a list item that opens with a verb where the others open with a noun phrase, or in another tense once a decisions table turns it on.

## What it catches

A list item that opens with a verb where the others open with a noun phrase, or in another tense.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Google's developer style guide (developers.google.com/style/lists) asks for "the same syntax/structure for all list items in a given list", and Microsoft's (learn.microsoft.com/style-guide/scannable-content/lists) for items "consistent in structure", in capitalization and end punctuation alike. This repository already holds a guide's numbered steps to an imperative opener (step-verb) and a commit subject to the imperative mood (subject-mood); this is the same expectation for any list. Each item's first word is tagged in its sentence by the part-of-speech tagger, which is accurate on sentences but guesses on fragments ("Fixed", "Cache", "Set"), so only a list of 3 or more tagged items is judged, a form must hold two thirds of them, and only the items outside it are named. An adjective opens a noun phrase ("Fast builds"), so it counts as one. Tense is judged per list, so a current role's list may stay in the present while the rest are past. Precision is unmeasured, with no labeled firing yet, so it ships off.

## Default decision

The code is `PRF1061`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
| `principle`          | off     |
| `guide`              | off     |
| `change-description` | off     |
| `agent-brief`        | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |
| `review-summary`     | off     |
| `review-inline`      | off     |
| `review-reply`       | off     |
| `agent-reply`        | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"list-parallel": "deny"},
  "paths": {"blog/**": {"list-parallel": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "list-parallel", "code": "PRF1061", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/list-parallel/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
