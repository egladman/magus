---
title: MemoryService
generated_from: reference/api/
description: MemoryService lists, upserts, and deletes memory records, plus reads/overwrites the singleton cursor snapshot.
tags: [api, proto, connect, grpc, memoryservice]
---

# MemoryService

MemoryService lists, upserts, and deletes memory records, plus reads/overwrites the singleton cursor snapshot.

Package `magus.memory.v1alpha1`, defined in `proto/magus/memory/v1alpha1/memory.proto`. Source: [memory.proto:31](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L31). Part of the [daemon API](../../index.md).

## Methods

### ListMemories

ListMemories returns every record in full (records are small). Paginated by contract so growth never forces a breaking change, though the store returns all records today.

`POST /magus.memory.v1alpha1.MemoryService/ListMemories`: unary. Source: [memory.proto:34](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L34).

Takes [ListMemoriesRequest](#listmemoriesrequest), returns [ListMemoriesResponse](#listmemoriesresponse).

### UpdateMemory

UpdateMemory upserts a record by name: with allow\_missing=true it creates the record when absent, otherwise it updates in place. An empty update\_mask is a full replace.

`POST /magus.memory.v1alpha1.MemoryService/UpdateMemory`: unary. Source: [memory.proto:37](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L37).

Takes [UpdateMemoryRequest](#updatememoryrequest), returns [Memory](#memory).

### DeleteMemory

DeleteMemory removes a record by name. With allow\_missing=true, deleting an absent record succeeds as a no-op (idempotent).

`POST /magus.memory.v1alpha1.MemoryService/DeleteMemory`: unary. Source: [memory.proto:40](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L40).

Takes [DeleteMemoryRequest](#deletememoryrequest), returns [DeleteMemoryResponse](#deletememoryresponse).

### GetCursor

GetCursor returns the cursor snapshot, empty when never written.

`POST /magus.memory.v1alpha1.MemoryService/GetCursor`: unary. Source: [memory.proto:42](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L42).

Takes [GetCursorRequest](#getcursorrequest), returns [Cursor](#cursor).

### UpdateCursor

UpdateCursor overwrites the cursor snapshot.

`POST /magus.memory.v1alpha1.MemoryService/UpdateCursor`: unary. Source: [memory.proto:44](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L44).

Takes [UpdateCursorRequest](#updatecursorrequest), returns [Cursor](#cursor).

## Messages

### Cursor

Cursor is the singleton "where did I leave off" snapshot. A singleton per AIP-156: read with GetCursor, overwritten with UpdateCursor, never listed and never created.

Source: [memory.proto:123](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L123).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `content` | string | 1 | UNTRUSTED; empty when the cursor was never written |

Used by: [GetCursor (response)](memory.md#getcursor), [UpdateCursor (response)](memory.md#updatecursor).

### DeleteMemoryRequest

Source: [memory.proto:110](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L110).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | _string.min_len: 1_ Non-empty: an empty name identifies no record, and allow\_missing would then swallow it as a silent no-op rather than reporting the mistake. |
| `allow_missing` | bool | 2 | true => deleting an absent record is a no-op |

Used by: [DeleteMemory (request)](memory.md#deletememory).

### DeleteMemoryResponse

Source: [memory.proto:117](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L117).

No fields.

Used by: [DeleteMemory (response)](memory.md#deletememory).

### GetCursorRequest

Source: [memory.proto:119](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L119).

No fields.

Used by: [GetCursor (request)](memory.md#getcursor).

### ListMemoriesRequest

Source: [memory.proto:87](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L87).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `page_size` | int32 | 1 | _int32.lte: 1000; int32.gte: 0_ Bounded at the same tier as ListActivity rather than left open: the store ignores the field today, so the ceiling costs nothing now and is the request a paginating store would have to honor later. Not shared with the other list RPCs as a common message - AIP-158 keeps these flat, and a shared one could carry only a single ceiling for every caller (ListEvents deliberately allows 5000). |
| `page_token` | string | 2 |  |

Used by: [ListMemories (request)](memory.md#listmemories).

### ListMemoriesResponse

Source: [memory.proto:97](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L97).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `memories` | [repeated Memory](#memory) | 1 |  |
| `next_page_token` | string | 2 | always empty until the store paginates |

Used by: [ListMemories (response)](memory.md#listmemories).

### Memory

Memory is one record. name is the kebab-slug identity; refs are the required payload; body is the caption (decision/plan only); status is the optional lifecycle field; references links to other records by name. create\_time/update\_time are output-only.

Source: [memory.proto:76](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L76).

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

Source: [memory.proto:68](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L68).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `kind` | [MemoryRefKind](#memoryrefkind) | 1 |  |
| `target` | string | 2 | node id / path, output ref token, or a raw query/command string |

Used by: [ListMemories (response)](memory.md#listmemories), [UpdateMemory (request)](memory.md#updatememory), [UpdateMemory (response)](memory.md#updatememory).

### UpdateCursorRequest

Source: [memory.proto:127](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L127).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `content` | string | 1 |  |

Used by: [UpdateCursor (request)](memory.md#updatecursor).

### UpdateMemoryRequest

Source: [memory.proto:102](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L102).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `memory` | [Memory](#memory) | 1 | _required_ Required: the payload IS the request. An upsert with no memory names nothing to create and carries nothing to write, so it can only be a client bug. |
| `update_mask` | FieldMask | 2 | empty = full replace (the only mode today) |
| `allow_missing` | bool | 3 | true => create when absent (AIP-134 upsert) |

Used by: [UpdateMemory (request)](memory.md#updatememory).

## Enums

### MemoryRefKind

MemoryRefKind is the closed set a ref points at. node/doc/output name a magus-domain node; query/command are re-runnable strings. Every kind resolves or dangles.

Source: [memory.proto:58](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L58).

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

Source: [memory.proto:49](https://github.com/egladman/magus/blob/main/proto/magus/memory/v1alpha1/memory.proto#L49).

| Value | # | Description |
|-------|---|-------------|
| `MEMORY_TYPE_UNSPECIFIED` | 0 |  |
| `MEMORY_TYPE_POINTER` | 1 |  |
| `MEMORY_TYPE_DECISION` | 2 |  |
| `MEMORY_TYPE_PLAN` | 3 |  |

Used by: [ListMemories (response)](memory.md#listmemories), [UpdateMemory (request)](memory.md#updatememory), [UpdateMemory (response)](memory.md#updatememory).

