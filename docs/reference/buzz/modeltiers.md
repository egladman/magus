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

bounds caps the context a routine worker may reach. It caps no tier's

**Signature:** `modeltiers\bounds(tier) -> magus\agent\ModelTierBounds`

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `tier`    | `ModelTier` |          |             |

**Returns:** magus\agent\ModelTierBounds

### skills

skills is the skill variant a tier's workers load. Routine gets the

**Signature:** `modeltiers\skills(tier) -> magus\agent\SkillVariant`

| Parameter | Type        | Optional | Description |
| --------- | ----------- | -------- | ----------- |
| `tier`    | `ModelTier` |          |             |

**Returns:** magus\agent\SkillVariant

