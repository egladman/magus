---
title: "message-length"
description: "A deny rule by default: it refuses a message longer than its rune cap, 160 unless the caller names one."
tags: [proofread, rules, message-length, deny]
aliases: [reference/prose/message-length]
---

# message-length

A deny rule by default: it refuses a message longer than its rune cap, 160 unless the caller names one.

## What it catches

A message longer than its rune cap, 160 unless the caller names one.

## Why

A message is read in a terminal at the moment something went wrong: about two lines hold a verdict, one command and a ref, and the rationale belongs behind the ref.

## Default decision

The code is `PRF9001`. The decision depends on the kind of text judged:

| Kind      | Default |
| --------- | ------- |
| `message` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"message-length": "advise"},
  "paths": {"blog/**": {"message-length": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "message-length", "code": "PRF9001", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/message-length/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
