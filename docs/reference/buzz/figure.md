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

| Parameter | Type          | Optional | Description |
| --------- | ------------- | -------- | ----------- |
| `dirs`    | `[magus\Dir]` |          |             |
| `drop`    | `[magus\Dir]` |          |             |

**Returns:** [magus\Dir]

### external

external is an actor the figure draws but no directory holds. look defaults to

**Signature:** `figure\external(name, [sub], [tag], [link], [look]) -> Actor`

| Parameter | Type    | Optional | Description |
| --------- | ------- | -------- | ----------- |
| `name`    | `str`   |          |             |
| `sub`     | `str`   | yes      |             |
| `tag`     | `str`   | yes      |             |
| `link`    | `str`   | yes      |             |
| `look`    | `Look?` | yes      |             |

**Returns:** Actor

### of

of starts an empty figure. direction defaults to Direction.across.

**Signature:** `figure\of(id, [title], [eyebrow], [desc], [direction]) -> mut Figure`

| Parameter   | Type         | Optional | Description |
| ----------- | ------------ | -------- | ----------- |
| `id`        | `str`        |          |             |
| `title`     | `str`        | yes      |             |
| `eyebrow`   | `str`        | yes      |             |
| `desc`      | `str`        | yes      |             |
| `direction` | `Direction?` | yes      |             |

**Returns:** mut Figure

### draw

draw lays f out and paints it with theme. anchorHref is a URL template: {path} takes a

**Signature:** `figure\draw(f, theme, [anchorHref]) -> str`

| Parameter    | Type     | Optional | Description |
| ------------ | -------- | -------- | ----------- |
| `f`          | `Figure` |          |             |
| `theme`      | `Theme`  |          |             |
| `anchorHref` | `str`    | yes      |             |

**Returns:** str

### drawDiagram

drawDiagram paints d, a Figure's diagram(), with theme, as draw

**Signature:** `figure\drawDiagram(d, theme, [anchorHref]) -> str`

| Parameter    | Type      | Optional | Description |
| ------------ | --------- | -------- | ----------- |
| `d`          | `Diagram` |          |             |
| `theme`      | `Theme`   |          |             |
| `anchorHref` | `str`     | yes      |             |

**Returns:** str

### nodeFact

nodeFact is the line a figure's stamp records for n. A box anchoring a symbol records the

**Signature:** `figure\nodeFact(n) -> str`

| Parameter | Type   | Optional | Description |
| --------- | ------ | -------- | ----------- |
| `n`       | `Node` |          |             |

**Returns:** str

