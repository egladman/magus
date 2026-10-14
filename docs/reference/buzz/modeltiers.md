---
title: modeltiers module
generated_from: reference/buzz/
aliases: [modules/modeltiers]
description: "The default model-tier declaration: the ModelTier enum, cheapest first, its default, and the bounds and skill variant of each tier."
tags: [modeltiers, module, stdlib, magusfile]
---

# modeltiers

The default model-tier declaration: the ModelTier enum, cheapest first, its default, and the bounds and skill variant of each tier.

> **Naming convention:** import the module under its bare name (`import "modeltiers"`), reach members with a backslash, and call methods in `camelCase`: `modeltiers\someMethod`.

## Methods

### bounds

bounds caps the context an economy worker may reach. It caps no tier's live jobs: a

**Signature:** `modeltiers\bounds(tier) -> magus\ModelTierBounds`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `tier`    | `any` |          |             |

**Returns:** any

### skills

skills is the skill variant a tier's workers load. Economy gets the full bodies: a

**Signature:** `modeltiers\skills(tier) -> magus\SkillVariant`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `tier`    | `any` |          |             |

**Returns:** any

