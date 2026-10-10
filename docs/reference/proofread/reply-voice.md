---
title: "reply-voice"
description: "A deny rule by default: it refuses text that answers a prompt the reader never saw: a reply opener, a bold-label item, a stock label."
tags: [proofread, rules, reply-voice, deny]
aliases: [reference/prose/reply-voice]
---

# reply-voice

A deny rule by default: it refuses text that answers a prompt the reader never saw: a reply opener, a bold-label item, a stock label.

## What it catches

Text that answers a prompt the reader never saw: a reply opener, a bold-label item, a stock label.

## Why

"Great question", "as discussed", a `**Cache:**` bullet or a `Summary` heading is the shape of an answer to a prompt the reader was not shown, so the reader has to reconstruct it. A review reply is one person answering another in a thread they share, so its openers and references to the thread are left alone.

## Default decision

The code is `PRF1002`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |
| `review-reply`       | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"reply-voice": "advise"},
  "paths": {"blog/**": {"reply-voice": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "reply-voice", "code": "PRF1002", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/reply-voice/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
