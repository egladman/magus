---
title: figure module
generated_from: reference/buzz/
aliases: [modules/figure]
description: "Architecture figures drawn from graph records: boxes and groups over Dir records, coverage by scope, edges derived from imports and declared calls."
tags: [figure, module, stdlib, magusfile]
---

# figure

Architecture figures drawn from graph records: boxes and groups over Dir records, coverage by scope, edges derived from imports and declared calls.

> **Naming convention:** import the module under its bare name (`import "figure"`), reach members with a backslash, and call methods in `camelCase`: `figure\someMethod`.

## Methods

### without

without is dirs less drop, in the order dirs holds them.

**Signature:** `figure\without(dirs, drop) -> [magus\Dir]`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `dirs`    | `any` |          |             |
| `drop`    | `any` |          |             |

**Returns:** any

### external

external is an actor the figure draws but no directory holds. look defaults to

**Signature:** `figure\external(name, [sub], [tag], [link], [look]) -> Actor`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `name`    | `any` |          |             |
| `sub`     | `any` | yes      |             |
| `tag`     | `any` | yes      |             |
| `link`    | `any` | yes      |             |
| `look`    | `any` | yes      |             |

**Returns:** any

### of

of starts an empty figure. direction defaults to Direction.across; generated marks a

**Signature:** `figure\of(id, [title], [eyebrow], [desc], [direction], [generated]) -> mut Figure`

| Parameter   | Type  | Optional | Description |
| ----------- | ----- | -------- | ----------- |
| `id`        | `any` |          |             |
| `title`     | `any` | yes      |             |
| `eyebrow`   | `any` | yes      |             |
| `desc`      | `any` | yes      |             |
| `direction` | `any` | yes      |             |
| `generated` | `any` | yes      |             |

**Returns:** any

### draw

draw lays f out and paints it with theme. anchorHref is a URL template: {path} takes a

**Signature:** `figure\draw(f, theme, [anchorHref]) -> str`

| Parameter    | Type  | Optional | Description |
| ------------ | ----- | -------- | ----------- |
| `f`          | `any` |          |             |
| `theme`      | `any` |          |             |
| `anchorHref` | `any` | yes      |             |

**Returns:** any

