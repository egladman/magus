---
title: "compact-filler"
description: "A rule that is off by default: it reports articles, linking verbs, \"you\", \"please\", \"note that\" and \"instead\" outside code, counted with the tokens dropping them saves once a decisions table turns it on."
tags: [proofread, rules, compact-filler, off]
aliases: [reference/prose/compact-filler]
---

# compact-filler

A rule that is off by default: it reports articles, linking verbs, "you", "please", "note that" and "instead" outside code, counted with the tokens dropping them saves once a decisions table turns it on.

## What it catches

Articles, linking verbs, "you", "please", "note that" and "instead" outside code, counted with the tokens dropping them saves.

## Dimension

`economy`: words that carry nothing. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

Each of these words is one token in tiktoken o200k and cl100k (Claude's tokenizer is not public), so the saving is counted at one token a word. They are what the deletion-only compressors drop first: LLMLingua-2 (Pan et al., Findings ACL 2024) holds BBH at 70.02 against 70.07 at 3x by deleting words alone. An eval over magus tasks ran the skills rewritten to this floor against their short and full forms: the rewrite was shorter than both and passed at least as often as the full form. Stopword removal past that floor hurts code tasks (Yang et al., arXiv 2410.22793), so the rule names only these words and never a negation, an obligation, a scope, a condition or an order. The measurements are in the machine-text research (research-machine-text.md in the proofread roadmap).

## Default decision

The code is `PRF7010`. The decision depends on the kind of text judged:

| Kind            | Default |
| --------------- | ------- |
| `agent-compact` | off     |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"compact-filler": "deny"},
  "paths": {"blog/**": {"compact-filler": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "compact-filler", "code": "PRF7010", "decision": "off", "url": "https://eli.gladman.cc/magus/reference/proofread/compact-filler/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/proofread/README.md#findings) - the fields every finding carries
