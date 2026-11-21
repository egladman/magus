---
title: diagram module
generated_from: reference/buzz/
aliases: [modules/diagram]
description: "Architecture diagrams drawn from graph records: boxes and groups over Dir records, coverage by scope, edges derived from imports and declared calls."
tags: [diagram, module, stdlib, magusfile]
---

# diagram

Architecture diagrams drawn from graph records: boxes and groups over Dir records, coverage by scope, edges derived from imports and declared calls.

> **Naming convention:** import the module under its bare name (`import "diagram"`), reach members with a backslash, and call methods in `camelCase`: `diagram\someMethod`.

## Methods

### without

without is dirs less drop, in the order dirs holds them.

**Signature:** `diagram\without(dirs, drop) -> result\Result::<[magus\Dir]>`

| Parameter | Type          | Optional | Description |
| --------- | ------------- | -------- | ----------- |
| `dirs`    | `[magus\Dir]` |          |             |
| `drop`    | `[magus\Dir]` |          |             |

**Returns:** result\Result::<[magus\Dir]>

### person

person is someone outside the code: a user, a reviewer, an operator. look defaults to

**Signature:** `diagram\person(name, [sub], [tag], [link], [look]) -> Actor`

| Parameter | Type    | Optional | Description |
| --------- | ------- | -------- | ----------- |
| `name`    | `str`   |          |             |
| `sub`     | `str`   | yes      |             |
| `tag`     | `str`   | yes      |             |
| `link`    | `str`   | yes      |             |
| `look`    | `Look?` | yes      |             |

**Returns:** Actor

### external

external is something outside the code the diagram draws: a vendor service, a host, a

**Signature:** `diagram\external(name, [sub], [tag], [link], [look]) -> Actor`

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

**Signature:** `diagram\step(name, [sub], [tag], [link], [look]) -> Actor`

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

**Signature:** `diagram\stage(dir, refs, [name], [sub], [tag], [look]) -> Actor`

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

of starts an empty diagram. direction defaults to Direction.across. level is the one level

**Signature:** `diagram\of(id, [title], [eyebrow], [desc], [direction], [level]) -> mut Diagram`

| Parameter   | Type         | Optional | Description |
| ----------- | ------------ | -------- | ----------- |
| `id`        | `str`        |          |             |
| `title`     | `str`        | yes      |             |
| `eyebrow`   | `str`        | yes      |             |
| `desc`      | `str`        | yes      |             |
| `direction` | `Direction?` | yes      |             |
| `level`     | `Level?`     | yes      |             |

**Returns:** mut Diagram

### draw

draw lays f out and paints it with theme. anchorHref is a URL template: {path} takes a

**Signature:** `diagram\draw(f, theme, [anchorHref]) -> result\Result::<str>`

| Parameter    | Type      | Optional | Description |
| ------------ | --------- | -------- | ----------- |
| `f`          | `Diagram` |          |             |
| `theme`      | `Theme`   |          |             |
| `anchorHref` | `str`     | yes      |             |

**Returns:** result\Result::<str>

### drawLayout

drawLayout paints d, a Diagram's layout(), with theme, as draw

**Signature:** `diagram\drawLayout(d, theme, [anchorHref], [drillHref]) -> result\Result::<str>`

| Parameter    | Type     | Optional | Description |
| ------------ | -------- | -------- | ----------- |
| `d`          | `Layout` |          |             |
| `theme`      | `Theme`  |          |             |
| `anchorHref` | `str`    | yes      |             |
| `drillHref`  | `str`    | yes      |             |

**Returns:** result\Result::<str>

### nodeFact

nodeFact is the line a diagram's stamp records for n. A box anchoring a symbol records the

**Signature:** `diagram\nodeFact(n) -> str`

| Parameter | Type   | Optional | Description |
| --------- | ------ | -------- | ----------- |
| `n`       | `Node` |          |             |

**Returns:** str

### drillLinks

drillLinks maps the id of each box in d that drills down to the diagram it links to.

**Signature:** `diagram\drillLinks(d) -> {str:str}`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `d`       | `Layout` |          |             |

**Returns:** {str:str}

### claims

claims counts d's connectors by what each asserts: "imports" and "calls" the graph holds,

**Signature:** `diagram\claims(d) -> {str:int}`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `d`       | `Layout` |          |             |

**Returns:** {str:int}

### atlas

atlas reads diagrams together. column is the widest a diagram may lay out, in SVG units:

**Signature:** `diagram\atlas(diagrams, [column]) -> Atlas`

| Parameter  | Type        | Optional | Description |
| ---------- | ----------- | -------- | ----------- |
| `diagrams` | `[Diagram]` |          |             |
| `column`   | `int`       | yes      |             |

**Returns:** Atlas

