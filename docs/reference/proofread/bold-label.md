---
title: "bold-label"
description: "An advisory by default: it explains, and blocks nothing, on list items or paragraphs of an agent reply that open with a bold label."
tags: [proofread, rules, bold-label, advise]
aliases: [reference/prose/bold-label]
---

# bold-label

An advisory by default: it explains, and blocks nothing, on list items or paragraphs of an agent reply that open with a bold label.

## What it catches

List items or paragraphs of an agent reply that open with a bold label.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A label turns each point into a field of a form, so the person reads the labels and rebuilds the sentences. Measured 2026-10-10 over 400 end-of-turn replies sampled from one machine's coding-agent sessions (101 projects, median 266 words): it fires on 121 (30.3%). Judged as review-reply, bold labels were the largest share of the 85 denials in 200 replies. It reports the first label and counts the rest. It advises: a long reply may need a label to be scanned. Firings are counted, not labeled.

## Default decision

The code is `PRF8020`. The decision depends on the kind of text judged:

| Kind          | Default |
| ------------- | ------- |
| `agent-reply` | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"bold-label": "deny"},
  "paths": {"blog/**": {"bold-label": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "bold-label", "code": "PRF8020", "decision": "advise", "url": "https://eli.gladman.cc/magus/reference/proofread/bold-label/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
