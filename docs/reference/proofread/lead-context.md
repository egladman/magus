---
title: "lead-context"
description: "A deny rule by default: it refuses a change description that does not open with a paragraph naming what a reader can now do."
tags: [proofread, rules, lead-context, deny]
aliases: [reference/prose/lead-context]
---

# lead-context

A deny rule by default: it refuses a change description that does not open with a paragraph naming what a reader can now do.

## What it catches

A change description that does not open with a paragraph naming what a reader can now do.

## Why

A reviewer's first question is what the change is for; a list, a heading or a reply opener in that place answers a different one. A lead under 12 words carries no reason. A lead whose first sentence states a defect and names no outcome only advises: over the 190 leads of the last 200 merged pull requests, 60 opened on the defect, and the outcome may be phrased in words no list holds.

## Default decision

The code is `PRF1001`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"lead-context": "advise"},
  "paths": {"blog/**": {"lead-context": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "lead-context", "code": "PRF1001", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/lead-context/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
