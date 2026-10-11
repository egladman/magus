---
title: "attribution"
description: "A house-style rule, off until a decisions table turns it on: it reports credit to a tool or an agent, or an account of how the work was produced."
tags: [proofread, rules, attribution, off]
aliases: [reference/prose/attribution]
---

# attribution

A house-style rule, off until a decisions table turns it on: it reports credit to a tool or an agent, or an account of how the work was produced.

## What it catches

Credit to a tool or an agent, or an account of how the work was produced.

## Why

House style: whether a description names the tools behind it is a team's call. This repository's product is about agents, so its word lists exempt that subject matter on pages; another repository would draw the line elsewhere.

## Default decision

The code is `PRF5005`. The decision depends on the kind of text judged:

| Kind                 | Default |
| -------------------- | ------- |
| `reference`          | off     |
| `guide`              | off     |
| `change-description` | off     |
| `agent-instructions` | off     |
| `commit-message`     | off     |
| `issue`              | off     |
| `release-notes`      | off     |
| `changelog`          | off     |
| `review-reply`       | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"attribution": "deny"},
  "paths": {"blog/**": {"attribution": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "attribution", "code": "PRF5005", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/attribution/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
