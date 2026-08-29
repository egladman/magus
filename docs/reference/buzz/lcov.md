---
title: lcov module
generated_from: reference/buzz/
aliases: [modules/lcov]
description: "LCOV coverage reports: the percentage a badge or a floor gate shows, and the line-level merge that keeps it true across multiple test processes."
tags: [lcov, module, stdlib, magusfile]
---

# lcov

LCOV coverage reports: the percentage a badge or a floor gate shows, and the line-level merge that keeps it true across multiple test processes.

> **Naming convention:** import the module under its bare name (`import "lcov"`), reach members with a backslash, and call methods in `camelCase`: `lcov\someMethod`.

## Methods

### percent

percent is the covered-line percentage across an lcov report, as a string

**Signature:** `lcov\percent(lcov, keep) -> str`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `lcov` | `any` |  | |
| `keep` | `any` |  | |

**Returns:** any

### mergePercent

mergePercent is percent over SEVERAL lcov reports of the same sources,

**Signature:** `lcov\mergePercent(reports, keep) -> str`

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `reports` | `any` |  | |
| `keep` | `any` |  | |

**Returns:** any

