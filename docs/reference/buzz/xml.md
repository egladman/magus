---
title: xml module
aliases: [modules/xml]
description: Build, serialize, and parse XML/SVG.
tags: [xml, module, stdlib, magusfile]
---

# xml

Build, serialize, and parse XML/SVG.

> **Naming convention:** import the module under its bare name (`import "xml"`) and call methods in `camelCase` (`xml.someMethod`).

## Methods

### render

Serialize an XML node to a string. A node is a string (text) or an element map {"tag": name, "attrs": [name, value, ...], "children": [node, ...]}. Empty-children elements self-close; no whitespace is emitted between tags.

**Signature:** `xml.render(node) → string` · [source](https://github.com/egladman/magus/blob/main/std/xml.go#L75)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `node` | `any` |  | |

**Returns:** string

### element

Build an element node from a tag, a flat [name, value, ...] attribute list, and a list of child nodes (elements or strings). Sugar for the {"tag", "attrs", "children"} map that render consumes.

**Signature:** `xml.element(tag, attrs, children) → any` · [source](https://github.com/egladman/magus/blob/main/std/xml.go#L62)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `tag` | `string` |  | |
| `attrs` | `[]string` |  | |
| `children` | `any` |  | |

**Returns:** any

### parse

Parse an XML string into a node tree: each element becomes {"tag": name, "attrs": [name, value, ...], "children": [node, ...]}, character data becomes a string. The inverse shape of render.

**Signature:** `xml.parse(s) → any` · [source](https://github.com/egladman/magus/blob/main/std/xml.go#L133)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** any

