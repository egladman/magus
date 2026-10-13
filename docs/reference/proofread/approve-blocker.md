---
title: "approve-blocker"
description: "An advisory by default: it explains, and blocks nothing, on an approving review whose body names a blocker (\"must fix\", \"blocking\", \"before merge\")."
tags: [proofread, rules, approve-blocker, advise]
aliases: [reference/prose/approve-blocker]
---

# approve-blocker

An advisory by default: it explains, and blocks nothing, on an approving review whose body names a blocker ("must fix", "blocking", "before merge").

## What it catches

An approving review whose body names a blocker ("must fix", "blocking", "before merge").

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Google's review guide (eng-practices, "The Standard of Code Review") approves a change once it improves code health and labels what is left as optional. An approval that names a blocker lets the change merge with the blocker in it, or leaves the author unsure the approval holds. "non-blocking" and "no blockers" are left alone. It needs the review's event (-event), and judges nothing without it. It is a convention, not a study, so it advises.

## Default decision

The code is `PRF8033`. The decision depends on the kind of text judged:

| Kind             | Default |
| ---------------- | ------- |
| `review-summary` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"approve-blocker": "deny"},
  "paths": {"blog/**": {"approve-blocker": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "approve-blocker", "code": "PRF8033", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/approve-blocker/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
