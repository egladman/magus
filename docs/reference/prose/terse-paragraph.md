---
title: "terse-paragraph"
description: "A house-style rule, off until a decisions table turns it on: it reports a paragraph or list item of agent instructions over 60 words."
tags: [prose, rules, terse-paragraph, off]
---

# terse-paragraph

A house-style rule, off until a decisions table turns it on: it reports a paragraph or list item of agent instructions over 60 words.

## What it catches

A paragraph or list item of agent instructions over 60 words.

## Why

House style: over the same skills 642 paragraphs and items ran p50 27 words, p90 64; caps near p95 trimmed only 5.5% of the bytes, so the cap sits below p90.

## Default decision

The code is `PRS7002`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `agent-instructions` | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"terse-paragraph": "deny"},
  "paths": {"blog/**": {"terse-paragraph": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "terse-paragraph", "code": "PRS7002", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/terse-paragraph/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
