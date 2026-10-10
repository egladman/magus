---
title: "chatbot"
description: "A deny rule by default: it refuses text a chat assistant addressed to its user: an offer, flattery, a knowledge disclaimer."
tags: [prose, rules, chatbot, deny]
---

# chatbot

A deny rule by default: it refuses text a chat assistant addressed to its user: an offer, flattery, a knowledge disclaimer.

## What it catches

Text a chat assistant addressed to its user: an offer, flattery, a knowledge disclaimer.

## Why

"I hope this helps" and "great question" answer a chat the reader never saw. Zero hits over the docs pages, changelog fragments and merged pull requests; the letter patterns ("Dear", "I am writing to") judge only a change description, which is never a letter.

## Default decision

The code is `PRS4004`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |
| `review-reply`       | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"chatbot": "advise"},
  "paths": {"blog/**": {"chatbot": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "chatbot", "code": "PRS4004", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/chatbot/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
