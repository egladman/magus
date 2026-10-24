---
title: "date-format"
description: "A rule that is off by default: it reports dates in more than one style in one text: 2026-10-13, Oct 2026, October 2026, 10/13/2026, or Sep beside Sept once a decisions table turns it on."
tags: [proofread, rules, date-format, off]
aliases: [reference/prose/date-format]
---

# date-format

A rule that is off by default: it reports dates in more than one style in one text: 2026-10-13, Oct 2026, October 2026, 10/13/2026, or Sep beside Sept once a decisions table turns it on.

## What it catches

Dates in more than one style in one text: 2026-10-13, Oct 2026, October 2026, 10/13/2026, or Sep beside Sept.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A reader compares dates by their form, and a mix makes each one a fresh parse; 10/13/2026 also reads two ways across locales. This repository's first decision record mixes May 2026 and Sept 2026 with 2026-09-19, where every later record writes ISO 8601 alone. May is both the full and the abbreviated name, so it takes no style; a date in code or quotes is not judged. The dates outside the text's most common style are reported. It is a convention, not a study, and no labeled firing yet, so it ships off and a decisions table turns it on.

## Default decision

The code is `PRF5007`. The decision depends on the kind of text judged:

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
  "rules": {"date-format": "deny"},
  "paths": {"blog/**": {"date-format": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "date-format", "code": "PRF5007", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/date-format/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
