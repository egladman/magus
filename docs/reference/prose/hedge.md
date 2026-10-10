---
title: "hedge"
description: "A deny rule by default: it refuses a softener qualifying a claim (\"might fix\", \"could potentially\")."
tags: [prose, rules, hedge, deny]
---

# hedge

A deny rule by default: it refuses a softener qualifying a claim ("might fix", "could potentially").

## What it catches

A softener qualifying a claim ("might fix", "could potentially").

## Why

A hedge lets a claim stand with no evidence. A writer who is unsure scopes the claim instead: a sentence under a "Not verified" heading, or one opening with "Not measured" or "Untested", states a limit and is exempt.

## Default decision

The code is `PRS3002`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. The prose judge reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"hedge": "advise"},
  "paths": {"blog/**": {"hedge": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "hedge", "code": "PRS3002", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/prose/hedge/"}
```

The judge's `-catalog` flag prints this entry with every other rule's.

## See also

- [All prose rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
