---
title: html module
generated_from: reference/buzz/
aliases: [modules/html]
description: HTML, XML and SVG text escaping.
tags: [html, module, stdlib, magusfile]
---

# html

HTML, XML and SVG text escaping.

> **Naming convention:** import the module under its bare name (`import "html"`), reach members with a backslash, and call methods in `camelCase`: `html\someMethod`.

## Methods

### escape

Replace & < > " ' in s with entities; valid in element text and in a quoted attribute of HTML, XML or SVG.

**Signature:** `html\escape(s) -> string` - [source](https://github.com/egladman/magus/blob/main/std/html.go)

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `s`       | `string` |          |             |

**Returns:** string

