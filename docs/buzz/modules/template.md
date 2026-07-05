---
title: template module
aliases: [modules/template]
description: Logic-less Mustache templating (Mustache spec, via github.com/cbroglie/mustache).
tags: [template, module, stdlib, magusfile]
---

# template

Logic-less Mustache templating (Mustache spec, via github.com/cbroglie/mustache).

> **Naming convention:** import the module under its bare name (`import "template"`) and call methods in `camelCase` (`template.someMethod`).

## Methods

### render

Render a Mustache template against a context value (usually a name->value map; lists drive sections, absent/false keys hide them). Returns the filled string; errors on a malformed template.

**Signature:** `template.render(template, data) → string` · [source](https://github.com/egladman/magus/blob/main/std/template.go#L36)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `template` | `string` |  | |
| `data` | `any` |  | |

**Returns:** string

