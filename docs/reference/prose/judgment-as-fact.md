---
title: "judgment-as-fact"
description: "An advisory by default: it explains, and blocks nothing, on a recommendation in a review reply stated as a fact, with no reason given."
tags: [prose, rules, judgment-as-fact, advise]
---

# judgment-as-fact

An advisory by default: it explains, and blocks nothing, on a recommendation in a review reply stated as a fact, with no reason given.

## What it catches

A recommendation in a review reply stated as a fact, with no reason given.

## Why

"This should be a map" leaves the author to guess why; the reason, or a label saying it is the writer's call, invites an answer. It advises: a modal also states requirements.

## Default decision

The code is `PRS8002`. The decision depends on the kind of text judged:

| Kind           | Default |
| -------------- | ------- |
| `review-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"judgment-as-fact": "deny"},
  "paths": {"blog/**": {"judgment-as-fact": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "judgment-as-fact", "code": "PRS8002", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/prose/judgment-as-fact/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
