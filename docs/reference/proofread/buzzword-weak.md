---
title: "buzzword-weak"
description: "An advisory by default: it explains, and blocks nothing, on a buzzword that also has an ordinary sense (\"crucial\", \"landscape\")."
tags: [proofread, rules, buzzword-weak, advise]
aliases: [reference/prose/buzzword-weak]
---

# buzzword-weak

An advisory by default: it explains, and blocks nothing, on a buzzword that also has an ordinary sense ("crucial", "landscape").

## What it catches

A buzzword that also has an ordinary sense ("crucial", "landscape").

## Why

These words are tells in a cluster and plain words alone, so the rule advises: one tell proves nothing.

## Default decision

The code is `PRF4007`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | advise  |
| `guide`              | advise  |
| `change-description` | advise  |
| `agent-instructions` | advise  |
| `review-reply`       | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"buzzword-weak": "deny"},
  "paths": {"blog/**": {"buzzword-weak": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "buzzword-weak", "code": "PRF4007", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/buzzword-weak/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
