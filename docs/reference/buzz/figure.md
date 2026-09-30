---
title: figure module
generated_from: reference/buzz/
aliases: [modules/figure]
description: "Architecture figures drawn from graph records: boxes and groups over Dir and Layer sets, coverage by set, edges derived from imports and declared calls."
tags: [figure, module, stdlib, magusfile]
---

# figure

Architecture figures drawn from graph records: boxes and groups over Dir and Layer sets, coverage by set, edges derived from imports and declared calls.

> **Naming convention:** import the module under its bare name (`import "figure"`), reach members with a backslash, and call methods in `camelCase`: `figure\someMethod`.

## Methods

### setOf

setOf is the set of the given dirs; a dir named twice is held once. Raises when an item

**Signature:** `figure\setOf(dirs) -> DirSet`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `dirs`    | `any` |          |             |

**Returns:** any

### layerSet

layerSet is every directory layer covers. Raises when layer is not a Layer record.

**Signature:** `figure\layerSet(layer) -> DirSet`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `layer`   | `any` |          |             |

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

of starts an empty left-to-right figure. The collections are built here because a mut

**Signature:** `figure\of(id) -> mut Figure`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `id`      | `any` |          |             |

**Returns:** any

