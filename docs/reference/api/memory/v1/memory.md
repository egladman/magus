---
title: MemoryService
generated_from: reference/api/
description: MemoryService lists, upserts, and deletes memory records, plus reads/overwrites the singleton cursor snapshot.
tags: [api, proto, connect, grpc, memoryservice]
---

# MemoryService

MemoryService lists, upserts, and deletes memory records, plus reads/overwrites the singleton cursor snapshot.

Package `magus.memory.v1`, defined in `proto/magus/memory/v1/memory.proto`. Source: [memory.proto:30](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L30). Part of the [daemon API](../../index.md).

## Methods

### ListMemories

ListMemories returns every record in full (records are small). Paginated by contract so growth never forces a breaking change, though the store returns all records today.

`POST /magus.memory.v1.MemoryService/ListMemories`: unary. Source: [memory.proto:33](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L33).

Takes [ListMemoriesRequest](#listmemoriesrequest), returns [ListMemoriesResponse](#listmemoriesresponse).

### UpdateMemory

UpdateMemory upserts a record by name: with allow\_missing=true it creates the record when absent, otherwise it updates in place. An empty update\_mask is a full replace.

`POST /magus.memory.v1.MemoryService/UpdateMemory`: unary. Source: [memory.proto:36](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L36).

Takes [UpdateMemoryRequest](#updatememoryrequest), returns [UpdateMemoryResponse](#updatememoryresponse).

### DeleteMemory

DeleteMemory removes a record by name. With allow\_missing=true, deleting an absent record succeeds as a no-op (idempotent).

`POST /magus.memory.v1.MemoryService/DeleteMemory`: unary. Source: [memory.proto:39](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L39).

Takes [DeleteMemoryRequest](#deletememoryrequest), returns [DeleteMemoryResponse](#deletememoryresponse).

### GetCursor

GetCursor returns the cursor snapshot, empty when never written.

`POST /magus.memory.v1.MemoryService/GetCursor`: unary. Source: [memory.proto:41](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L41).

Takes [GetCursorRequest](#getcursorrequest), returns [GetCursorResponse](#getcursorresponse).

### UpdateCursor

UpdateCursor overwrites the cursor snapshot.

`POST /magus.memory.v1.MemoryService/UpdateCursor`: unary. Source: [memory.proto:43](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L43).

Takes [UpdateCursorRequest](#updatecursorrequest), returns [UpdateCursorResponse](#updatecursorresponse).

## Messages

### DeleteMemoryRequest

Source: [memory.proto:106](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L106).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 |  |
| `allow_missing` | bool | 2 | true => deleting an absent record is a no-op |

Used by: [DeleteMemory (request)](memory.md#deletememory).

### DeleteMemoryResponse

Source: [memory.proto:111](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L111).

No fields.

Used by: [DeleteMemory (response)](memory.md#deletememory).

### GetCursorRequest

Source: [memory.proto:113](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L113).

No fields.

Used by: [GetCursor (request)](memory.md#getcursor).

### GetCursorResponse

Source: [memory.proto:115](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L115).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `content` | string | 1 | UNTRUSTED; empty when the cursor was never written |

Used by: [GetCursor (response)](memory.md#getcursor).

### ListMemoriesRequest

Source: [memory.proto:86](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L86).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `page_size` | int32 | 1 | wired for forward-compat; the store returns all records today |
| `page_token` | string | 2 |  |

Used by: [ListMemories (request)](memory.md#listmemories).

### ListMemoriesResponse

Source: [memory.proto:91](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L91).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `memories` | [repeated Memory](#memory) | 1 |  |
| `next_page_token` | string | 2 | always empty until the store paginates |

Used by: [ListMemories (response)](memory.md#listmemories).

### Memory

Memory is one record. name is the kebab-slug identity; refs are the required payload; body is the caption (decision/plan only); status is the optional lifecycle field; references links to other records by name. create\_time/update\_time are output-only.

Source: [memory.proto:75](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L75).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 |  |
| `type` | [MemoryType](#memorytype) | 2 |  |
| `refs` | [repeated MemoryRef](#memoryref) | 3 |  |
| `status` | string | 4 | free lifecycle string (accepted, superseded, done, stale, ...) |
| `body` | string | 5 | UNTRUSTED prose caption; empty for a pointer |
| `references` | repeated string | 6 | other record names (memory -> memory) |
| `create_time` | Timestamp | 7 | output only |
| `update_time` | Timestamp | 8 | output only |

Used by: [ListMemories (response)](memory.md#listmemories), [UpdateMemory (request)](memory.md#updatememory), [UpdateMemory (response)](memory.md#updatememory).

### MemoryRef

MemoryRef is one typed pointer: the payload of a record.

Source: [memory.proto:67](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L67).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `kind` | [MemoryRefKind](#memoryrefkind) | 1 |  |
| `target` | string | 2 | node id / path, output ref token, or a raw query/command string |

Used by: [ListMemories (response)](memory.md#listmemories), [UpdateMemory (request)](memory.md#updatememory), [UpdateMemory (response)](memory.md#updatememory).

### UpdateCursorRequest

Source: [memory.proto:119](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L119).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `content` | string | 1 |  |

Used by: [UpdateCursor (request)](memory.md#updatecursor).

### UpdateCursorResponse

Source: [memory.proto:123](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L123).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `content` | string | 1 |  |

Used by: [UpdateCursor (response)](memory.md#updatecursor).

### UpdateMemoryRequest

Source: [memory.proto:96](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L96).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `memory` | [Memory](#memory) | 1 | memory.name is the identity to upsert |
| `update_mask` | FieldMask | 2 | empty = full replace (the only mode today) |
| `allow_missing` | bool | 3 | true => create when absent (AIP-134 upsert) |

Used by: [UpdateMemory (request)](memory.md#updatememory).

### UpdateMemoryResponse

Source: [memory.proto:102](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L102).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `memory` | [Memory](#memory) | 1 | the stored record, with server-set timestamps |

Used by: [UpdateMemory (response)](memory.md#updatememory).

## Enums

### MemoryRefKind

MemoryRefKind is the closed set a ref points at. node/doc/output name a magus-domain node; query/command are re-runnable strings. Every kind resolves or dangles.

Source: [memory.proto:57](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L57).

| Value | # | Description |
|-------|---|-------------|
| `MEMORY_REF_KIND_UNSPECIFIED` | 0 |  |
| `MEMORY_REF_KIND_QUERY` | 1 |  |
| `MEMORY_REF_KIND_NODE` | 2 |  |
| `MEMORY_REF_KIND_OUTPUT` | 3 |  |
| `MEMORY_REF_KIND_COMMAND` | 4 |  |
| `MEMORY_REF_KIND_DOC` | 5 |  |

Used by: [ListMemories (response)](memory.md#listmemories), [UpdateMemory (request)](memory.md#updatememory), [UpdateMemory (response)](memory.md#updatememory).

### MemoryType

MemoryType is the subject axis of a record (stable, closed). pointer carries refs only; decision and plan additionally carry a prose caption (the why the graph cannot derive).

Source: [memory.proto:48](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1/memory.proto#L48).

| Value | # | Description |
|-------|---|-------------|
| `MEMORY_TYPE_UNSPECIFIED` | 0 |  |
| `MEMORY_TYPE_POINTER` | 1 |  |
| `MEMORY_TYPE_DECISION` | 2 |  |
| `MEMORY_TYPE_PLAN` | 3 |  |

Used by: [ListMemories (response)](memory.md#listmemories), [UpdateMemory (request)](memory.md#updatememory), [UpdateMemory (response)](memory.md#updatememory).

