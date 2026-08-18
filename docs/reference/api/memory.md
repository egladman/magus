---
title: MemoryService
description: MemoryService lists, upserts, and deletes memory records, plus reads/overwrites the singleton cursor snapshot.
tags: [api, proto, connect, grpc, memoryservice]
---

# MemoryService

MemoryService lists, upserts, and deletes memory records, plus reads/overwrites the singleton cursor snapshot.

Package `magus.memory.v1alpha1`, defined in `proto/magus/memory/v1alpha1/memory.proto`. Part of the [daemon API](index.md).

## Methods

### ListMemories

ListMemories returns every record in full (records are small). Paginated by contract so growth never forces a breaking change, though the store returns all records today.

`POST /magus.memory.v1alpha1.MemoryService/ListMemories`: unary.

Takes `ListMemoriesRequest`, returns `ListMemoriesResponse`.

### UpdateMemory

UpdateMemory upserts a record by name: with allow\_missing=true it creates the record when absent, otherwise it updates in place. An empty update\_mask is a full replace.

`POST /magus.memory.v1alpha1.MemoryService/UpdateMemory`: unary.

Takes `UpdateMemoryRequest`, returns `Memory`.

### DeleteMemory

DeleteMemory removes a record by name. With allow\_missing=true, deleting an absent record succeeds as a no-op (idempotent).

`POST /magus.memory.v1alpha1.MemoryService/DeleteMemory`: unary.

Takes `DeleteMemoryRequest`, returns `DeleteMemoryResponse`.

### GetCursor

GetCursor returns the cursor snapshot, empty when never written.

`POST /magus.memory.v1alpha1.MemoryService/GetCursor`: unary.

Takes `GetCursorRequest`, returns `Cursor`.

### UpdateCursor

UpdateCursor overwrites the cursor snapshot.

`POST /magus.memory.v1alpha1.MemoryService/UpdateCursor`: unary.

Takes `UpdateCursorRequest`, returns `Cursor`.

## Messages

### Cursor

Cursor is the singleton "where did I leave off" snapshot. A singleton per AIP-156: read with GetCursor, overwritten with UpdateCursor, never listed and never created.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `content` | string | 1 | UNTRUSTED; empty when the cursor was never written |

### DeleteMemoryRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 |  |
| `allow_missing` | bool | 2 | true => deleting an absent record is a no-op |

### DeleteMemoryResponse

No fields.

### GetCursorRequest

No fields.

### ListMemoriesRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `page_size` | int32 | 1 | wired for forward-compat; the store returns all records today |
| `page_token` | string | 2 |  |

### ListMemoriesResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `memories` | repeated Memory | 1 |  |
| `next_page_token` | string | 2 | always empty until the store paginates |

### Memory

Memory is one record. name is the kebab-slug identity; refs are the required payload; body is the caption (decision/plan only); status is the optional lifecycle field; references links to other records by name. create\_time/update\_time are output-only.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 |  |
| `type` | MemoryType | 2 |  |
| `refs` | repeated MemoryRef | 3 |  |
| `status` | string | 4 | free lifecycle string (accepted, superseded, done, stale, ...) |
| `body` | string | 5 | UNTRUSTED prose caption; empty for a pointer |
| `references` | repeated string | 6 | other record names (memory -> memory) |
| `create_time` | Timestamp | 7 | output only |
| `update_time` | Timestamp | 8 | output only |

### MemoryRef

MemoryRef is one typed pointer: the payload of a record.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `kind` | MemoryRefKind | 1 |  |
| `target` | string | 2 | node id / path, output ref token, or a raw query/command string |

### UpdateCursorRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `content` | string | 1 |  |

### UpdateMemoryRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `memory` | Memory | 1 | memory.name is the identity to upsert |
| `update_mask` | FieldMask | 2 | empty = full replace (the only mode today) |
| `allow_missing` | bool | 3 | true => create when absent (AIP-134 upsert) |

## Enums

### MemoryRefKind

MemoryRefKind is the closed set a ref points at. node/doc/output name a magus-domain node; query/command are re-runnable strings. Every kind resolves or dangles.

| Value | # | Description |
|-------|---|-------------|
| `MEMORY_REF_KIND_UNSPECIFIED` | 0 |  |
| `MEMORY_REF_KIND_QUERY` | 1 |  |
| `MEMORY_REF_KIND_NODE` | 2 |  |
| `MEMORY_REF_KIND_OUTPUT` | 3 |  |
| `MEMORY_REF_KIND_COMMAND` | 4 |  |
| `MEMORY_REF_KIND_DOC` | 5 |  |

### MemoryType

MemoryType is the subject axis of a record (stable, closed). pointer carries refs only; decision and plan additionally carry a prose caption (the why the graph cannot derive).

| Value | # | Description |
|-------|---|-------------|
| `MEMORY_TYPE_UNSPECIFIED` | 0 |  |
| `MEMORY_TYPE_POINTER` | 1 |  |
| `MEMORY_TYPE_DECISION` | 2 |  |
| `MEMORY_TYPE_PLAN` | 3 |  |

