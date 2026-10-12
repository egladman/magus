---
title: "issue-repro"
description: "A deny rule by default: it refuses a bug report with neither what happened against what was expected, nor steps to reproduce it."
tags: [proofread, rules, issue-repro, advise]
aliases: [reference/prose/issue-repro]
---

# issue-repro

A deny rule by default: it refuses a bug report with neither what happened against what was expected, nor steps to reproduce it.

## What it catches

A bug report with neither what happened against what was expected, nor steps to reproduce it.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A maintainer cannot start on a defect they cannot see. A title with a defect word ("crash", "fails", "regression") that is followed by no "expected", "actual", "observed", "steps to reproduce" or "instead of" sends the first reply to asking for them. It advises: the title is read for the defect and the body for the cue, and either can be phrased another way. Neither of this repository's two issues has a defect title, so it measured no firing. Measured 2026-10-11 over the adjudicated labeling round 2, on pull requests read as issues: 26.2% precision (11 of 42) before, 84.6% (11 of 13) after. A title whose type or opening verb names other work ("test:", "Add a regression test"), whose defect word names a topic ("crash detection") or sits in code is no report, and a body that says what should happen, what happened in the past tense, numbered steps or a quote of the original report shows the defect. It stays off until a fresh labeling round measures it.

## Default decision

The code is `PRF1020`. The decision depends on the kind of text judged:

| Kind    | Default |
| ------- | ------- |
| `issue` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"issue-repro": "deny"},
  "paths": {"blog/**": {"issue-repro": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "issue-repro", "code": "PRF1020", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/issue-repro/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
