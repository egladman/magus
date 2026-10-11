---
title: "banned-word"
description: "A house-style rule, off until a decisions table turns it on: it reports a word this repository bans, in a stylesheet comment."
tags: [proofread, rules, banned-word, off]
aliases: [reference/prose/banned-word]
---

# banned-word

A house-style rule, off until a decisions table turns it on: it reports a word this repository bans, in a stylesheet comment.

## What it catches

A word this repository bans, in a stylesheet comment.

## Dimension

`conventions`: a house or genre convention is broken. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: the banned words of hack/lint/banned-words.buzz, one of them only as a noun. Each names nothing, and the fix says what the thing is. A word in backticks or double quotes is a mention.

## Default decision

The code is `PRF5006`. The decision depends on the kind of text judged:

| Kind         | Default |
| ------------ | ------- |
| `stylesheet` | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"banned-word": "deny"},
  "paths": {"blog/**": {"banned-word": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "banned-word", "code": "PRF5006", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/banned-word/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
