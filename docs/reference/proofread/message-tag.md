---
title: "message-tag"
description: "A deny rule by default: it refuses a message opening with a component tag (\"server: \") or carrying a marker such as \"[AGENT]\"."
tags: [proofread, rules, message-tag, deny]
aliases: [reference/prose/message-tag]
---

# message-tag

A deny rule by default: it refuses a message opening with a component tag ("server: ") or carrying a marker such as "[AGENT]".

## What it catches

A message opening with a component tag ("server: ") or carrying a marker such as "[AGENT]".

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A tag names who spoke, which the reader already knows, and pushes the verdict off the start of the line.

## Default decision

The code is `PRF9004`. The decision depends on the kind of text judged:

| Kind      | Default |
| --------- | ------- |
| `message` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"message-tag": "advise"},
  "paths": {"blog/**": {"message-tag": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "message-tag", "code": "PRF9004", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/message-tag/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
