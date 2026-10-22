---
title: "lead-context"
description: "A deny rule by default: it refuses a change description whose first paragraph does not state the intent: what it set out to do and why."
tags: [proofread, rules, lead-context, deny]
aliases: [reference/prose/lead-context]
---

# lead-context

A deny rule by default: it refuses a change description whose first paragraph does not state the intent: what it set out to do and why.

## What it catches

A change description whose first paragraph does not state the intent: what it set out to do and why.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A reviewer reads the intent first, then what was built, then where the work left the plan (a Cut, Changed from plan, Not done or Not verified bullet or heading). A code block, heading, list, reply opener or a lead under 12 words is denied. A lead that opens on a defect passes when a purpose follows within two sentences: 60 of the 190 leads of the last 200 merged pull requests opened on one.

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
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
