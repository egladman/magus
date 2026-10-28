---
title: diskwatch module
generated_from: reference/buzz/
aliases: [modules/diskwatch]
description: "The reclaim hook's cheap half: sample free disk space into a per-user store at most once per disk.sample_every, and decide from the last two samples whether to start a reclaim c..."
tags: [diskwatch, module, stdlib, magusfile]
---

# diskwatch

The reclaim hook's cheap half: sample free disk space into a per-user store at most once per disk.sample_every, and decide from the last two samples whether to start a reclaim chunk.

> **Naming convention:** import the module under its bare name (`import "diskwatch"`), reach members with a backslash, and call methods in `camelCase`: `diskwatch\someMethod`.

## Methods

### storePath

storePath is the store's path, creating its directory.

**Signature:** `diskwatch\storePath() -> str`

**Returns:** any

### thresholds

thresholds reads the disk section of `magus config view -o json`. A key the config does

**Signature:** `diskwatch\thresholds(config) -> Thresholds`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `config`  | `any` |          |             |

**Returns:** any

### due

due is the rate limit: a store last written at lastMs is sampled again only once everyMs

**Signature:** `diskwatch\due([lastMs], now, everyMs) -> bool`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `lastMs`  | `any` | yes      |             |
| `now`     | `any` |          |             |
| `everyMs` | `any` |          |             |

**Returns:** any

### decide

decide grades cur against the thresholds and prev, the last earlier sample of the same

**Signature:** `diskwatch\decide([prev], cur, t) -> Decision`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `prev`    | `any` | yes      |             |
| `cur`     | `any` |          |             |
| `t`       | `any` |          |             |

**Returns:** any

### chunkRunning

chunkRunning reports whether a chunk started within CHUNK_WINDOW_MS has not yet finished,

**Signature:** `diskwatch\chunkRunning(samples, now) -> bool`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `samples` | `any` |          |             |
| `now`     | `any` |          |             |

**Returns:** any

### lastOf

lastOf is the newest sample of path, null when the store holds none.

**Signature:** `diskwatch\lastOf(samples, path) -> DiskSample?`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `samples` | `any` |          |             |
| `path`    | `any` |          |             |

**Returns:** any

### line

line is what a hook prints for one sample: one line when it started a chunk or space is

**Signature:** `diskwatch\line(d, cur, started) -> str?`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `d`       | `any` |          |             |
| `cur`     | `any` |          |             |
| `started` | `any` |          |             |

**Returns:** any

### encode

**Signature:** `diskwatch\encode(s) -> str`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `s`       | `any` |          |             |

**Returns:** any

### decode

**Signature:** `diskwatch\decode(text) -> DiskSample`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `text`    | `any` |          |             |

**Returns:** any

### read

read decodes every line of the store at path; a missing store is empty.

**Signature:** `diskwatch\read(path) -> [DiskSample]`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `path`    | `any` |          |             |

**Returns:** any

### append

append adds s to the store, then compacts it by rename once it holds twice keepLines.

**Signature:** `diskwatch\append(path, s)`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `path`    | `any` |          |             |
| `s`       | `any` |          |             |

