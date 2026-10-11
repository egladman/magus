---
title: "hedge"
description: "A rule that denies on `reference`, `guide`, `change-description`, `agent-instructions`, `commit-message`, `issue`, `release-notes`, and `changelog` and advises on `agent-reply` by default: it reports a softener qualifying a claim (\"might fix\", \"could potentially\")."
tags: [proofread, rules, hedge, deny]
aliases: [reference/prose/hedge]
---

# hedge

A rule that denies on `reference`, `guide`, `change-description`, `agent-instructions`, `commit-message`, `issue`, `release-notes`, and `changelog` and advises on `agent-reply` by default: it reports a softener qualifying a claim ("might fix", "could potentially").

## What it catches

A softener qualifying a claim ("might fix", "could potentially").

## Dimension

`evidence`: a claim says more or less than what was shown. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

A hedge lets a claim stand with no evidence. A writer who is unsure scopes the claim instead: a sentence under a "Not verified" heading, or one opening with "Not measured" or "Untested", states a limit and is exempt.

## Default decision

The code is `PRF3002`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | deny    |
| `guide`              | deny    |
| `change-description` | deny    |
| `agent-instructions` | deny    |
| `commit-message`     | deny    |
| `issue`              | deny    |
| `release-notes`      | deny    |
| `changelog`          | deny    |
| `agent-reply`        | advise  |

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
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
{"rule": "hedge", "code": "PRF3002", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/hedge/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
