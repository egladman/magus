---
title: "restates-diff"
description: "A rule that is off by default: it reports a bullet or paragraph that is a verb and the things touched, with no reason: \"Updates foo.go\" once a decisions table turns it on."
tags: [proofread, rules, restates-diff, off]
aliases: [reference/prose/restates-diff]
---

# restates-diff

A rule that is off by default: it reports a bullet or paragraph that is a verb and the things touched, with no reason: "Updates foo.go" once a decisions table turns it on.

## What it catches

A bullet or paragraph that is a verb and the things touched, with no reason: "Updates foo.go".

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

The diff already shows what was touched; a reader opening the description wants why. A sentence of a verb such as updates, adds or removes and nothing but code spans, paths and identifiers carries nothing the file list lacks. A reason marker (because, so, since, a colon, a purpose such as "to avoid") or any other content word passes. No firing has been labeled, so it ships off.

## Default decision

The code is `PRF4032`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `change-description` | off     |
| `review-summary`     | off     |
| `review-inline`      | off     |
| `review-reply`       | off     |
| `commit-message`     | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"restates-diff": "deny"},
  "paths": {"blog/**": {"restates-diff": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "restates-diff", "code": "PRF4032", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/restates-diff/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
