---
title: csv module
aliases: [modules/csv]
description: Delimiter-separated tabular text (CSV, TSV) parsing and rendering.
tags: [csv, module, stdlib, magusfile]
---

# csv

Delimiter-separated tabular text (CSV, TSV) parsing and rendering.

> **Naming convention:** import the module under its bare name (`import "csv"`), reach members with a backslash, and call methods in `camelCase`: `csv\someMethod`.

## Methods

### parse

Parse delimiter-separated text into a list of rows, each a list of fields. Quoted fields, embedded delimiters, and embedded newlines are handled per RFC 4180. delimiter defaults to "," - pass "\t" for TSV. When comment is a single character, lines starting with it are skipped. Raises when a row has a different field count than the first, which is the corruption a hand-rolled split would silently pass through.

**Signature:** `csv\parse(s, [delimiter], [comment]) → [][]string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/csv/csv.go#L80)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `s` | `string` |  | |
| `delimiter` | `string` | yes | |
| `comment` | `string` | yes | |

**Returns:** [][]string

### stringify

Render rows (a list of lists of fields) as delimiter-separated text, quoting any field that needs it. delimiter defaults to ",". The output ends with a newline, so it is ready to write.

**Signature:** `csv\stringify(rows, [delimiter]) → string` · [source](https://github.com/egladman/magus/blob/main/std/encoding/csv/csv.go#L105)

| Parameter | Type | Optional | Description |
|-----------|------|----------|-------------|
| `rows` | `[][]string` |  | |
| `delimiter` | `string` | yes | |

**Returns:** string

