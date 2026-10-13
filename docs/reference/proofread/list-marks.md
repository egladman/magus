---
title: "list-marks"
description: "A rule that is off by default: it reports a list item whose initial capital or closing full stop differs from the list's other items once a decisions table turns it on."
tags: [proofread, rules, list-marks, off]
aliases: [reference/prose/list-marks]
---

# list-marks

A rule that is off by default: it reports a list item whose initial capital or closing full stop differs from the list's other items once a decisions table turns it on.

## What it catches

A list item whose initial capital or closing full stop differs from the list's other items.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Google's developer style guide (developers.google.com/style/lists) asks for "the same syntax/structure for all list items in a given list", and Microsoft's (learn.microsoft.com/style-guide/scannable-content/lists) for items "consistent in structure", in capitalization and end punctuation alike. A reader scanning a list reads a change of form as a change of meaning. Only a list of 3 or more items is judged, and an item that is only code or a link is skipped; so is a first word with a capital past its first letter (API, GitHub), which is a name. The items outside the most common form are reported, by name. It is a convention, not a study, and no labeled firing yet, so it ships off and a decisions table turns it on.

## Default decision

The code is `PRF1060`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
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
  "rules": {"list-marks": "deny"},
  "paths": {"blog/**": {"list-marks": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "list-marks", "code": "PRF1060", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/list-marks/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
