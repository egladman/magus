---
title: "template"
description: "A house-style rule, off until a decisions table turns it on: it reports an agent-instructions template that does not render, so neither of its forms can be judged."
tags: [proofread, rules, template, off]
aliases: [reference/prose/template]
---

# template

A house-style rule, off until a decisions table turns it on: it reports an agent-instructions template that does not render, so neither of its forms can be judged.

## What it catches

An agent-instructions template that does not render, so neither of its forms can be judged.

## Dimension

`structure`: the reader has to reconstruct the order or the purpose. A finding is counted on
the dimension of its rule, the cost it names to the reader.

## Why

House style: the template form is internal/agent's. A body that does not render is judged by this rule alone, and passes where it is off.

## Default decision

The code is `PRF7004`. The decision depends on the kind of text judged:

| Kind                          | Default |
| ----------------------------- | ------- |
| `agent-instructions-template` | off     |

House style ships `off`: the rule encodes one repository's conventions, not
a rule of writing a teammate reads. A repository turns it on in its decisions table.

## Changing it

A decisions table sets this rule to `deny`, `advise` or `off`, for everything judged
or for the files a glob matches. Proofread reads the table from the file its
`-decisions` flag names:

```json
{
  "rules": {"template": "deny"},
  "paths": {"blog/**": {"template": "off"}}
}
```

`rules` applies to every file. A glob in `paths` applies to the files it matches, and the
last match wins. A rule set to `off` never reports.

## Seeing it

A finding names the rule, its code and the decision that applies, and links here:

```json
{"rule": "template", "code": "PRF7004", "decision": "deny", "url": "https://eli.gladman.cc/magus/reference/proofread/template/"}
```

`proofread rules` prints this entry with every other rule's.

## See also

- [All proofread rules](index.md) - every rule, grouped by what it checks
- [Writing rules](../../conventions.md#writing-rules) - how the rules apply to this repository's pages and pull requests
- [The findings contract](https://github.com/egladman/magus/blob/main/libs/conventions/readme.md#findings) - the fields every finding carries
