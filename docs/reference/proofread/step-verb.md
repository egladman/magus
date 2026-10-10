---
title: "step-verb"
description: "A deny rule by default: it refuses a numbered step of a guide that does not open with its imperative verb."
tags: [proofread, rules, step-verb, deny]
aliases: [reference/prose/step-verb]
---

# step-verb

A deny rule by default: it refuses a numbered step of a guide that does not open with its imperative verb.

## What it catches

A numbered step of a guide that does not open with its imperative verb.

## Why

A reader following a procedure scans for the action. A numbered list counts as a procedure only when one of its items opens with an imperative, so a recap, a precedence order or a list of reasons is left alone.

## Default decision

The code is `PRF1004`. The decision depends on the kind of text judged:

| Kind    | Default |
| ------- | ------- |
| `guide` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"step-verb": "advise"},
  "paths": {"blog/**": {"step-verb": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "step-verb", "code": "PRF1004", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/step-verb/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
