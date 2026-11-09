---
title: diskwatch module
generated_from: reference/buzz/
aliases: [modules/diskwatch]
description: "The cheap half of disk reclaiming: the per-user store of free-space samples, what a sample asks for, the fence a cache trim keeps, and the lines a person reads."
tags: [diskwatch, module, stdlib, magusfile]
---

# diskwatch

The cheap half of disk reclaiming: the per-user store of free-space samples, what a sample asks for, the fence a cache trim keeps, and the lines a person reads.

> **Naming convention:** import the module under its bare name (`import "diskwatch"`), reach members with a backslash, and call methods in `camelCase`: `diskwatch\someMethod`.

## Methods

### storePath

storePath is the store's path, creating its directory.

**Signature:** `diskwatch\storePath() -> str`

**Returns:** str

### thresholds

thresholds reads the disk section of `magus config view -o json`. A key the config does

**Signature:** `diskwatch\thresholds(config) -> Thresholds`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `config`  | `str` |          |             |

**Returns:** Thresholds

### due

due is the rate limit: a store last written at lastMs is sampled again only once everyMs

**Signature:** `diskwatch\due([lastMs], now, everyMs) -> bool`

| Parameter | Type      | Optional | Description |
| --------- | --------- | -------- | ----------- |
| `lastMs`  | `double?` | yes      |             |
| `now`     | `double`  |          |             |
| `everyMs` | `double`  |          |             |

**Returns:** bool

### decide

decide grades cur against the thresholds and prev, the last earlier sample of the same

**Signature:** `diskwatch\decide([prev], cur, t) -> Decision`

| Parameter | Type          | Optional | Description |
| --------- | ------------- | -------- | ----------- |
| `prev`    | `DiskSample?` | yes      |             |
| `cur`     | `DiskSample`  |          |             |
| `t`       | `Thresholds`  |          |             |

**Returns:** Decision

### fenceMs

fenceMs is the fence a cache trim keeps for a sample, 0 when no cache may be

**Signature:** `diskwatch\fenceMs(cur, t) -> double`

| Parameter | Type         | Optional | Description |
| --------- | ------------ | -------- | ----------- |
| `cur`     | `DiskSample` |          |             |
| `t`       | `Thresholds` |          |             |

**Returns:** double

### chunkRunning

chunkRunning reports whether a chunk started within CHUNK_WINDOW_MS has not yet finished,

**Signature:** `diskwatch\chunkRunning(samples, now) -> bool`

| Parameter | Type           | Optional | Description |
| --------- | -------------- | -------- | ----------- |
| `samples` | `[DiskSample]` |          |             |
| `now`     | `double`       |          |             |

**Returns:** bool

### lastOf

lastOf is the newest sample of path, null when the store holds none.

**Signature:** `diskwatch\lastOf(samples, path) -> DiskSample?`

| Parameter | Type           | Optional | Description |
| --------- | -------------- | -------- | ----------- |
| `samples` | `[DiskSample]` |          |             |
| `path`    | `str`          |          |             |

**Returns:** DiskSample?

### personLine

personLine is what a person reads after a tick: what happened, what it did

**Signature:** `diskwatch\personLine(r) -> str`

| Parameter | Type     | Optional | Description |
| --------- | -------- | -------- | ----------- |
| `r`       | `Report` |          |             |

**Returns:** str

### criticalLine

criticalLine is the line for space below disk.critical_free_mb when

**Signature:** `diskwatch\criticalLine(cur) -> str`

| Parameter | Type         | Optional | Description |
| --------- | ------------ | -------- | ----------- |
| `cur`     | `DiskSample` |          |             |

**Returns:** str

### pendingLine

pendingLine is what the store has that no person was told: a tick's result,

**Signature:** `diskwatch\pendingLine(samples, now) -> str?`

| Parameter | Type           | Optional | Description |
| --------- | -------------- | -------- | ----------- |
| `samples` | `[DiskSample]` |          |             |
| `now`     | `double`       |          |             |

**Returns:** str?

### encode

**Signature:** `diskwatch\encode(s) -> str`

| Parameter | Type         | Optional | Description |
| --------- | ------------ | -------- | ----------- |
| `s`       | `DiskSample` |          |             |

**Returns:** str

### decode

**Signature:** `diskwatch\decode(text) -> DiskSample`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `text`    | `str` |          |             |

**Returns:** DiskSample

### read

read decodes every line of the store at path; a missing store is empty.

**Signature:** `diskwatch\read(path) -> [DiskSample]`

| Parameter | Type  | Optional | Description |
| --------- | ----- | -------- | ----------- |
| `path`    | `str` |          |             |

**Returns:** [DiskSample]

### append

append adds s to the store, then compacts it by rename once it holds twice keepLines.

**Signature:** `diskwatch\append(path, s)`

| Parameter | Type         | Optional | Description |
| --------- | ------------ | -------- | ----------- |
| `path`    | `str`        |          |             |
| `s`       | `DiskSample` |          |             |

