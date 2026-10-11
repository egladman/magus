---
title: "unbacked-done"
description: "An advisory by default: it explains, and blocks nothing, on a claim of done, fixed, verified or passing with no command, output ref, file or link beside it."
tags: [proofread, rules, unbacked-done, advise]
aliases: [reference/prose/unbacked-done]
---

# unbacked-done

An advisory by default: it explains, and blocks nothing, on a claim of done, fixed, verified or passing with no command, output ref, file or link beside it.

## What it catches

A claim of done, fixed, verified or passing with no command, output ref, file or link beside it.

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

"Done and verified" asks the person to trust a run they cannot open; a backticked command, a magus output ref or a file beside the claim lets them check it. Measured 2026-10-10 over the 400 replies: it fires on 55 (13.8%), "Done", "Fixed" and "gate green" most. A negated or conditional claim ("not verified", "once it passes") is left alone. It advises: the person may have watched the run, and evidence may sit in another paragraph.

## Default decision

The code is `PRF8024`. The decision depends on the kind of text judged:

| Kind          | Default |
| ------------- | ------- |
| `agent-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"unbacked-done": "deny"},
  "paths": {"blog/**": {"unbacked-done": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "unbacked-done", "code": "PRF8024", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/unbacked-done/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
