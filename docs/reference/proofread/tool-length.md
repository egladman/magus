---
title: "tool-length"
description: "An advisory by default: it explains, and blocks nothing, on a tool or skill description over 1024 runes."
tags: [proofread, rules, tool-length, advise]
aliases: [reference/prose/tool-length]
---

# tool-length

An advisory by default: it explains, and blocks nothing, on a tool or skill description over 1024 runes.

## What it catches

A tool or skill description over 1024 runes.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Every description is in the model's context in every session, whether or not the tool is called. Over the 22 descriptions magus ships the median is 586 runes (96 words), 20 sit between 98 and 704, and the 2 MCP tools client (1207) and diff (1144) run past 1024, the length the Agent Skills format allows a skill description. The house paragraph cap of 60 words (terse-paragraph) would fire on 19 of the 22, so it is not used here. The rule only advises while 2 firings are all there are.

## Default decision

The code is `PRF9022`. The decision depends on the kind of text judged:

| Kind               | Default |
| ------------------ | ------- |
| `tool-description` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"tool-length": "deny"},
  "paths": {"blog/**": {"tool-length": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "tool-length", "code": "PRF9022", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/tool-length/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
