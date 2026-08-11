---
title: url module
aliases: [modules/url]
description: URL percent-encoding, parsing, and building.
tags: [url, module, stdlib, magusfile]
---

# url

URL percent-encoding, parsing, and building.

> **Naming convention:** import the module under its bare name (`import "url"`), reach members with a backslash, and call methods in `camelCase`: `url\someMethod`.

## Methods

### encode

Percent-encode s for use in a URL query component.

**Signature:** `url\encode(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/url/url.go#L65)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### decode

Decode a percent-encoded URL query component; errors on malformed input.

**Signature:** `url\decode(s) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/url/url.go#L70)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |

**Returns:** string

### parse

Parse a URL string into {scheme, host, port, path, query, fragment}; errors on malformed input.

**Signature:** `url\parse(raw_url) → URL` · [source](https://github.com/egladman/magus/blob/main/std/encoding/url/url.go#L79)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `raw_url` | `string` |  | |

**Returns:** map[string]any

### build

Build a URL string from a URL object - the same shape parse returns, so the two round-trip. Missing fields are treated as empty.

**Signature:** `url\build(parts) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/url/url.go#L96)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `parts` | `map[string]any` |  | |

**Returns:** string

