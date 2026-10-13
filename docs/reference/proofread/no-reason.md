---
title: "no-reason"
description: "An advisory by default: it explains, and blocks nothing, on an inline review comment that asks for a change and gives no reason."
tags: [proofread, rules, no-reason, advise]
aliases: [reference/prose/no-reason]
---

# no-reason

An advisory by default: it explains, and blocks nothing, on an inline review comment that asks for a change and gives no reason.

## What it catches

An inline review comment that asks for a change and gives no reason.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Widyasari et al. (arXiv 2311.09020) labeled 793 Gerrit review comments and found 42% gave a suggestion with no explanation; Ebert et al. (SANER 2019) found missing rationale the most frequent reason for confusion in code review, which delays the merge. Only a comment of one sentence is judged, one opening with a command or holding should, must or needs to: a second sentence is read as the reason, and so is a reason marker (because, since, so, otherwise, which, a purpose such as "to avoid"). A question, and a comment labeled nit, typo or optional, is left alone. No firing has been labeled, so it advises.

## Default decision

The code is `PRF8030`. The decision depends on the kind of text judged:

| Kind            | Default |
| --------------- | ------- |
| `review-inline` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"no-reason": "deny"},
  "paths": {"blog/**": {"no-reason": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "no-reason", "code": "PRF8030", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/no-reason/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
