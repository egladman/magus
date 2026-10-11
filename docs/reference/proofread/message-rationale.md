---
title: "message-rationale"
description: "A deny rule by default: it refuses a message that joins more than one reason (so, because, a semicolon, \", which\")."
tags: [proofread, rules, message-rationale, deny]
aliases: [reference/prose/message-rationale]
---

# message-rationale

A deny rule by default: it refuses a message that joins more than one reason (so, because, a semicolon, ", which").

## What it catches

A message that joins more than one reason (so, because, a semicolon, ", which").

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

One reason names the cause; a second is an argument the reader did not ask for at the moment of the failure. The ref holds the rest.

## Default decision

The code is `PRF9002`. The decision depends on the kind of text judged:

| Kind      | Default |
| --------- | ------- |
| `message` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"message-rationale": "advise"},
  "paths": {"blog/**": {"message-rationale": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "message-rationale", "code": "PRF9002", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/message-rationale/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
