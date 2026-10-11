---
title: "tool-lead"
description: "An advisory by default: it explains, and blocks nothing, on a description whose first sentence opens on the tool itself, holds under 4 words or runs past 40 words."
tags: [proofread, rules, tool-lead, advise]
aliases: [reference/prose/tool-lead]
---

# tool-lead

An advisory by default: it explains, and blocks nothing, on a description whose first sentence opens on the tool itself, holds under 4 words or runs past 40 words.

## What it catches

A description whose first sentence opens on the tool itself, holds under 4 words or runs past 40 words.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A 2026 study of 856 MCP tool descriptions found 56% did not state their purpose clearly, and a model choosing among many tools has only the description to go on. Over the 22 descriptions magus ships (6 MCP tools, 16 skills) the first sentence runs 7 to 45 words and none opens on "This tool" or on the tool's own name. 2 pass 40 words: magus-context-audit (42) and magus-diagram (45), each a list of nouns before the verb's object. The rule only advises, since 2 firings are too few to deny on.

## Default decision

The code is `PRF9020`. The decision depends on the kind of text judged:

| Kind               | Default |
| ------------------ | ------- |
| `tool-description` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"tool-lead": "deny"},
  "paths": {"blog/**": {"tool-lead": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "tool-lead", "code": "PRF9020", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/tool-lead/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
