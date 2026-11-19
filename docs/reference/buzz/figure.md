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

### person

person is someone outside the code: a user, a reviewer, an operator. look defaults to

**Signature:** `figure\person(name, [sub], [tag], [link], [look]) -> Actor`

| Parameter | Type    | Optional | Description |
| --------- | ------- | -------- | ----------- |
| `name`    | `str`   |          |             |
| `sub`     | `str`   | yes      |             |
| `tag`     | `str`   | yes      |             |
| `link`    | `str`   | yes      |             |
| `look`    | `Look?` | yes      |             |

**Returns:** Actor

### external

external is something outside the code the figure draws: a vendor service, a host, a

**Signature:** `figure\external(name, [sub], [tag], [link], [look]) -> Actor`

| Parameter | Type    | Optional | Description |
| --------- | ------- | -------- | ----------- |
| `name`    | `str`   |          |             |
| `sub`     | `str`   | yes      |             |
| `tag`     | `str`   | yes      |             |
| `link`    | `str`   | yes      |             |
| `look`    | `Look?` | yes      |             |

**Returns:** Actor

### step

step is one step of a process no directory holds: a CI job, a decision, a role. It is

**Signature:** `figure\step(name, [sub], [tag], [link], [look]) -> Actor`

| Parameter | Type    | Optional | Description |
| --------- | ------- | -------- | ----------- |
| `name`    | `str`   |          |             |
| `sub`     | `str`   | yes      |             |
| `tag`     | `str`   | yes      |             |
| `link`    | `str`   | yes      |             |
| `look`    | `Look?` | yes      |             |

**Returns:** Actor

### stage

stage is a step that lives inside one package, named by the symbol that carries it out:

**Signature:** `figure\stage(dir, refs, [name], [sub], [tag], [look]) -> Actor`

| Parameter | Type               | Optional | Description |
| --------- | ------------------ | -------- | ----------- |
| `dir`     | `magus\Dir`        |          |             |
| `refs`    | `magus\RefsResult` |          |             |
| `name`    | `str`              | yes      |             |
| `sub`     | `str`              | yes      |             |
| `tag`     | `str`              | yes      |             |
| `look`    | `Look?`            | yes      |             |

**Returns:** Actor

### of

of starts an empty figure. direction defaults to Direction.across. level is the one level

**Signature:** `figure\of(id, [title], [eyebrow], [desc], [direction], [level]) -> mut Figure`

| Parameter   | Type         | Optional | Description |
| ----------- | ------------ | -------- | ----------- |
| `id`        | `str`        |          |             |
| `title`     | `str`        | yes      |             |
| `eyebrow`   | `str`        | yes      |             |
| `desc`      | `str`        | yes      |             |
| `direction` | `Direction?` | yes      |             |
| `level`     | `Level?`     | yes      |             |

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

**Signature:** `figure\drawDiagram(d, theme, [anchorHref], [drillHref]) -> str`

| Parameter    | Type      | Optional | Description |
| ------------ | --------- | -------- | ----------- |
| `d`          | `Diagram` |          |             |
| `theme`      | `Theme`   |          |             |
| `anchorHref` | `str`     | yes      |             |
| `drillHref`  | `str`     | yes      |             |

**Returns:** str

### nodeFact

nodeFact is the line a figure's stamp records for n. A box anchoring a symbol records the

**Signature:** `figure\nodeFact(n) -> str`

| Parameter | Type   | Optional | Description |
| --------- | ------ | -------- | ----------- |
| `n`       | `Node` |          |             |

**Returns:** str

### drillLinks

drillLinks maps the id of each box in d that drills down to the figure it links to.

**Signature:** `figure\drillLinks(d) -> {str:str}`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `d`       | `Diagram` |          |             |

**Returns:** {str:str}

### claims

claims counts d's connectors by what each asserts: "imports" and "calls" the graph holds,

**Signature:** `figure\claims(d) -> {str:int}`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `d`       | `Diagram` |          |             |

**Returns:** {str:int}

### atlas

atlas reads figures together. column is the widest a figure may lay out, in SVG units:

**Signature:** `figure\atlas(figures, [column]) -> Atlas`

| Parameter | Type       | Optional | Description |
| --------- | ---------- | -------- | ----------- |
| `figures` | `[Figure]` |          |             |
| `column`  | `int`      | yes      |             |

**Returns:** Atlas

