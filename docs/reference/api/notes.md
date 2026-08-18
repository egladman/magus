---
title: NotesService
description: "NotesService lists the workspace's notes and reads one in full."
tags: [api, proto, connect, grpc, notesservice]
---

# NotesService

NotesService lists the workspace's notes and reads one in full.

Package `magus.notes.v1alpha1`, defined in `proto/magus/notes/v1alpha1/notes.proto`. Part of the [daemon API](index.md).

## Methods

### ListNotes

ListNotes returns every note in both declared stores, each with its anchors already resolved. Paginated by contract so growth never forces a breaking change, though the store returns all notes today (bounded by the store's own scan cap).

`POST /magus.notes.v1alpha1.NotesService/ListNotes`: unary.

Takes `ListNotesRequest`, returns `ListNotesResponse`.

### GetNote

GetNote returns one note by scope and name, including its body.

`POST /magus.notes.v1alpha1.NotesService/GetNote`: unary.

Takes `GetNoteRequest`, returns `Note`.

## Messages

### Anchor

Anchor is one typed attachment from a note to a graph entity, with the result of checking it. degrades\_to names the coarser anchor a dangling one falls back to - the demotion is reported rather than guessed, because nothing here searches for a renamed symbol: a low-confidence match is worse than an admitted failure.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `kind` | AnchorKind | 1 |  |
| `target` | string | 2 |  |
| `status` | AnchorStatus | 3 |  |
| `node_id` | string | 4 | node\_id is the graph node this anchor names, so a client can link into the Graph Explorer. Empty when the anchor does not resolve. |
| `detail` | string | 5 | detail explains a DANGLING or DRIFTED status in one sentence, and what to do about it. Empty for the other statuses. UNTRUSTED only in the sense that it is prose; it is magus-authored, unlike body below. |

### GetNoteRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 |  |
| `scope` | Scope | 2 | scope disambiguates a name that exists in both stores. Required: guessing which store was meant is the mistake worth refusing, since the two mean different things to a reader. |

### ListNotesRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `page_size` | int32 | 1 | wired for forward-compat; the store returns all notes today |
| `page_token` | string | 2 |  |

### ListNotesResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `notes` | repeated Note | 1 |  |
| `next_page_token` | string | 2 | always empty until the store paginates |
| `stores` | repeated StoreStatus | 3 | stores reports both scopes whether or not either yielded a note, so a client can render the empty and undeclared cases honestly instead of showing a blank list. |

### Note

Note is one human-authored entry.  There is no author field, and its absence is deliberate rather than an omission: a self-attested author is forgeable by whatever wrote the file, and authorship already comes from git via the @vcs shard for the shared store. A client that wants to show who wrote a shared note should read the graph, not this field, because this field would be a claim the file makes about itself.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | name is the note's identity: its declared id when it has one, otherwise its path within the store. |
| `scope` | Scope | 2 |  |
| `title` | string | 3 |  |
| `tags` | repeated string | 4 |  |
| `anchors` | repeated Anchor | 5 |  |
| `body` | string | 6 | body is the note's prose. UNTRUSTED: a client must render it as TEXT, never as trusted HTML. Empty in ListNotes; GetNote fills it. |
| `path` | string | 7 | path is where the note lives on disk. Workspace-relative for a shared note, absolute for a private one, which is the same distinction the scope carries. |
| `staleness` | Staleness | 8 |  |
| `outrun_days` | int32 | 9 | outrun\_days is how far the subject moved ahead of the prose. The raw number rather than just the bucket, because it is the evidence: a UI ranking something down owes the reader "400 days behind its subject" rather than a silent reorder. Zero unless staleness is OUTRUN or PETRIFIED. |
| `modify_time` | Timestamp | 10 | modify\_time is the file's modification time, observed rather than stored, so it cannot disagree with the file it describes. Output only. |

### StoreStatus

StoreStatus reports one store's availability, so a client can tell "declared but empty" from "not declared at all" from "declared and broken" - three states that must not collapse into an empty list.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `scope` | Scope | 1 |  |
| `declared` | bool | 2 | declared is false when the workspace declares no path for this scope, which is the default and is not a failure. |
| `path` | string | 3 | path is the store's directory. Empty when it is not declared. |
| `note_count` | int32 | 4 | note\_count is how many notes this store contributed to the response. |
| `issues` | repeated string | 5 | issues are the problems found while scanning: a malformed entry, a truncated scan. Each is magus-authored prose naming one file and what to do about it. |

## Enums

### AnchorKind

AnchorKind is the closed set of things a note may attach to. Deliberately absent: any kind carrying a POSITION - a node id is checkable, so its breakage is reportable, while a line number changes on the next edit above it with nothing to detect.

| Value | # | Description |
|-------|---|-------------|
| `ANCHOR_KIND_UNSPECIFIED` | 0 |  |
| `ANCHOR_KIND_SYMBOL` | 1 |  |
| `ANCHOR_KIND_FILE` | 2 |  |
| `ANCHOR_KIND_PROJECT` | 3 |  |
| `ANCHOR_KIND_TARGET` | 4 |  |
| `ANCHOR_KIND_NOTE` | 5 |  |

### AnchorStatus

AnchorStatus is what checking one anchor against the workspace found.  DRIFTED is the case an existence check cannot catch and a reader cannot see: the anchored code still exists and quietly stopped meaning what the note says. UNVERIFIED is a real, distinct answer and must never be rendered as "fine" - it means no fingerprint was recorded (the note predates fingerprinting, or was never re-read) or none could be computed here, and reporting drift from missing data is the false positive that trains a reader to ignore every flag.

| Value | # | Description |
|-------|---|-------------|
| `ANCHOR_STATUS_UNSPECIFIED` | 0 |  |
| `ANCHOR_STATUS_RESOLVES` | 1 |  |
| `ANCHOR_STATUS_DANGLING` | 2 |  |
| `ANCHOR_STATUS_DRIFTED` | 3 |  |
| `ANCHOR_STATUS_UNVERIFIED` | 4 |  |

### Scope

Scope is which store a note lives in, and it is the axis a reader must never have to guess: the two mean different things about who can see the note.

| Value | # | Description |
|-------|---|-------------|
| `SCOPE_UNSPECIFIED` | 0 |  |
| `SCOPE_SHARED` | 1 | SHARED is the store inside the checkout: committed, reviewed, attributed by git. |
| `SCOPE_PRIVATE` | 2 | PRIVATE is this machine only. It is never exported to the shared cache and never served on the LAN share listener. |

### Staleness

Staleness is whether the prose was outrun by the thing it describes.  The signal is NOT calendar age: prose about a subsystem nobody has touched is current, and decaying it by age is how a signal earns the right to be ignored. What is measured is DIVERGENCE between two commit dates. UNMEASURED is not "fresh" - it is the honest answer when VCS history is unavailable, when the prose has no history, or when there is no subject to compare against. Every private note is UNMEASURED today, because staleness is keyed by workspace-relative path and a private note's source is absolute.

| Value | # | Description |
|-------|---|-------------|
| `STALENESS_UNSPECIFIED` | 0 |  |
| `STALENESS_UNMEASURED` | 1 |  |
| `STALENESS_CURRENT` | 2 |  |
| `STALENESS_OUTRUN` | 3 |  |
| `STALENESS_PETRIFIED` | 4 |  |

