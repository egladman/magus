---
title: "tool-boundary"
description: "An advisory by default: it explains, and blocks nothing, on a description that names no case where the tool is the wrong choice and no tool to use instead."
tags: [proofread, rules, tool-boundary, advise]
aliases: [reference/prose/tool-boundary]
---

# tool-boundary

An advisory by default: it explains, and blocks nothing, on a description that names no case where the tool is the wrong choice and no tool to use instead.

## What it catches

A description that names no case where the tool is the wrong choice and no tool to use instead.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Among many tools whose descriptions all say what they do, the one that also says when not to use it is the one the model can rule out. Over the 22 descriptions magus ships, 19 name a boundary ("Do NOT", "instead", "rather than", "last resort", "cannot", "use the client tool") and 3 do not: the status tool, magus-run and magus-workspace-rules. The rule only advises, since whether a boundary is worth stating depends on what the other tools do.

## Default decision

The code is `PRF9021`. The decision depends on the kind of text judged:

| Kind               | Default |
| ------------------ | ------- |
| `tool-description` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"tool-boundary": "deny"},
  "paths": {"blog/**": {"tool-boundary": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "tool-boundary", "code": "PRF9021", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/tool-boundary/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
