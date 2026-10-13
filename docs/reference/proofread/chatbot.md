---
title: "chatbot"
description: "A rule that is off by default: it reports text a chat assistant addressed to its user: an offer, flattery, a knowledge disclaimer once a decisions table turns it on."
tags: [proofread, rules, chatbot, advise]
aliases: [reference/prose/chatbot]
---

# chatbot

A rule that is off by default: it reports text a chat assistant addressed to its user: an offer, flattery, a knowledge disclaimer once a decisions table turns it on.

## What it catches

Text a chat assistant addressed to its user: an offer, flattery, a knowledge disclaimer.

## Dimension

`stance`: the text reads as a verdict on a person, or as addressed to someone else. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

"I hope this helps" and "great question" answer a chat the reader never saw. Zero hits over the docs pages, changelog fragments and merged pull requests; the letter patterns ("Dear", "I am writing to") judge only a change description, which is never a letter. Measured 2026-10-10 before a fix: 88.9% precision on its cases (8 of 9), firing on 0.3% of human review comments (1.2% of their replies), 4.2% of bot replies and 0.1% of AIDev pull requests; 29 of 30 human review-comment firings read were a person answering in the thread ("You're right, ...", "feel free to", "great catch"). A review reply's reader saw the chat, so there an agreement, an invitation, an offer and an answer's opener are left alone; "I hope this helps" and the disclaimers still count. Measured 2026-10-11 over the adjudicated labeling round: 30.8% precision (16 of 52) before, 100% (14 of 14) after. In a reply, "you're absolutely right", "great question" and "I'm sorry I" were wrong in all 21 labeled firings, so they count only on a page or in a description, and words quoted from an issue or a comment are left alone. It stays off until 30 labeled firings show it.

## Default decision

The code is `PRF4004`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
| `guide`              | off     |
| `change-description` | off     |
| `agent-instructions` | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |
| `review-reply`       | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"chatbot": "deny"},
  "paths": {"blog/**": {"chatbot": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "chatbot", "code": "PRF4004", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/chatbot/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/README.md#findings) - the fields every finding carries
