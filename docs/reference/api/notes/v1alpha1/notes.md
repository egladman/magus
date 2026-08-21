---
title: NotesService
generated_from: reference/api/
description: "NotesService lists the workspace's notes and reads one in full."
tags: [api, proto, connect, grpc, notesservice]
---

# NotesService

NotesService lists the workspace's notes and reads one in full.

Package `magus.notes.v1alpha1`, defined in `proto/magus/notes/v1alpha1/notes.proto`. Source: [notes.proto:32](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L32). Part of the [daemon API](../../index.md).

## Methods

### ListNotes

ListNotes returns every note in both declared stores, each with its anchors already resolved. Paginated by contract so growth never forces a breaking change, though the store returns all notes today (bounded by the store's own scan cap).

`POST /magus.notes.v1alpha1.NotesService/ListNotes`: unary. Source: [notes.proto:36](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L36).

Takes [ListNotesRequest](#listnotesrequest), returns [ListNotesResponse](#listnotesresponse).

### GetNote

GetNote returns one note by scope and name, including its body.

`POST /magus.notes.v1alpha1.NotesService/GetNote`: unary. Source: [notes.proto:38](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L38).

Takes [GetNoteRequest](#getnoterequest), returns [Note](#note).

## Messages

### Anchor

Anchor is one typed attachment from a note to a graph entity, with the result of checking it. degrades\_to names the coarser anchor a dangling one falls back to - the demotion is reported rather than guessed, because nothing here searches for a renamed symbol: a low-confidence match is worse than an admitted failure.

Source: [notes.proto:105](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L105).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `kind` | [AnchorKind](#anchorkind) | 1 |  |
| `target` | string | 2 |  |
| `status` | [AnchorStatus](#anchorstatus) | 3 |  |
| `node_id` | string | 4 | node\_id is the graph node this anchor names, so a client can link into the Graph Explorer. Empty when the anchor does not resolve. |
| `detail` | string | 5 | detail explains a DANGLING, DRIFTED or BODY\_CHANGED status in one sentence, and what to do about it. Empty for the other statuses. UNTRUSTED only in the sense that it is prose; it is magus-authored, unlike body below. |

Used by: [GetNote (response)](notes.md#getnote), [ListNotes (response)](notes.md#listnotes).

### GetNoteRequest

Source: [notes.proto:210](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L210).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | _string.pattern: `^(shared\|private)/.+$`_ The note's resource name, "shared/{note}" or "private/{note}". The store is part of the name rather than a second field because a name and a scope arriving separately are a compound key that can disagree, and the store is the axis a reader must never have to guess - the two mean different things about who can read the note. |

Used by: [GetNote (request)](notes.md#getnote).

### ListNotesRequest

Source: [notes.proto:195](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L195).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `page_size` | int32 | 1 | _int32.lte: 1000; int32.gte: 0_ Bounded at the same tier as ListActivity and ListMemories; see ListMemoriesRequest for why the three keep their own flat field rather than sharing a pagination message. |
| `page_token` | string | 2 |  |

Used by: [ListNotes (request)](notes.md#listnotes).

### ListNotesResponse

Source: [notes.proto:202](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L202).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `notes` | [repeated Note](#note) | 1 |  |
| `next_page_token` | string | 2 | always empty until the store paginates |
| `stores` | [repeated StoreStatus](#storestatus) | 3 | stores reports both scopes whether or not either yielded a note, so a client can render the empty and undeclared cases honestly instead of showing a blank list. |

Used by: [ListNotes (response)](notes.md#listnotes).

### Note

Note is one human-authored entry.

There is no author field, and its absence is deliberate rather than an omission: a self-attested author is forgeable by whatever wrote the file, and authorship already comes from git via the @vcs shard for the shared store. A client that wants to show who wrote a shared note should read the graph, not this field, because this field would be a claim the file makes about itself.

Source: [notes.proto:126](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L126).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | name is the note's identity: its declared id when it has one, otherwise its path within the store. |
| `scope` | [Scope](#scope) | 2 |  |
| `title` | string | 3 |  |
| `tags` | repeated string | 4 |  |
| `anchors` | [repeated Anchor](#anchor) | 5 |  |
| `body` | string | 6 | body is the note's prose. UNTRUSTED: a client must render it as TEXT, never as trusted HTML. Empty in ListNotes; GetNote fills it. |
| `path` | string | 7 | path is where the note lives on disk. Workspace-relative for a shared note, absolute for a private one, which is the same distinction the scope carries. |
| `staleness` | [Staleness](#staleness) | 8 |  |
| `outrun_days` | int32 | 9 | outrun\_days is how far the subject moved ahead of the prose. The raw number rather than just the bucket, because it is the evidence: a UI ranking something down owes the reader "400 days behind its subject" rather than a silent reorder. Zero unless staleness is OUTRUN or PETRIFIED. |
| `modify_time` | Timestamp | 10 | modify\_time is the file's modification time, observed rather than stored, so it cannot disagree with the file it describes. Output only. |
| `source` | [Source](#source) | 11 | source is set when the prose was quoted rather than written - a captured review thread. Absent on a note a person wrote, which is the overwhelming majority.  A client MUST render a note carrying this differently from one without it. The whole claim a note makes is that somebody stands behind it; a capture's claim is the opposite, that nobody does and the source can be re-read instead. A surface that presents the two identically tells the reader the one thing this field exists to prevent. |

Used by: [GetNote (response)](notes.md#getnote), [ListNotes (response)](notes.md#listnotes).

### Source

Source is the provenance of prose a note did not originate.

Source: [notes.proto:160](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L160).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `kind` | string | 1 | kind names what the note is a transcript OF ("review-thread").  A string and not an enum, deliberately. An enum rejects what it does not know, and the client that meets a kind its binary predates is far better served by showing the reader an unfamiliar word than by dropping the one field that says this prose is quoted. |
| `ref` | string | 2 | ref identifies the conversation within kind - a review session id. Opaque to a client. |
| `as_of` | string | 3 | as\_of is the subject's identity at capture time: for a review thread, the digest of the patch the comments were written against. It is what makes a stale capture detectable rather than merely old. |
| `captured` | Timestamp | 4 | captured is when the transcript was taken. NOT the file's modify\_time: editing a capture's surrounding prose moves the mtime and must not move this. |

Used by: [GetNote (response)](notes.md#getnote), [ListNotes (response)](notes.md#listnotes).

### StoreStatus

StoreStatus reports one store's availability, so a client can tell "declared but empty" from "not declared at all" from "declared and broken" - three states that must not collapse into an empty list.

Source: [notes.proto:181](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L181).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `scope` | [Scope](#scope) | 1 |  |
| `declared` | bool | 2 | declared is false when the workspace declares no path for this scope, which is the default and is not a failure. |
| `path` | string | 3 | path is the store's directory. Empty when it is not declared. |
| `note_count` | int32 | 4 | note\_count is how many notes this store contributed to the response. |
| `issues` | repeated string | 5 | issues are the problems found while scanning: a malformed entry, a truncated scan. Each is magus-authored prose naming one file and what to do about it. |

Used by: [ListNotes (response)](notes.md#listnotes).

## Enums

### AnchorKind

AnchorKind is the closed set of things a note may attach to. Deliberately absent: any kind carrying a POSITION - a node id is checkable, so its breakage is reportable, while a line number changes on the next edit above it with nothing to detect.

Source: [notes.proto:55](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L55).

| Value | # | Description |
|-------|---|-------------|
| `ANCHOR_KIND_UNSPECIFIED` | 0 |  |
| `ANCHOR_KIND_SYMBOL` | 1 |  |
| `ANCHOR_KIND_FILE` | 2 |  |
| `ANCHOR_KIND_PROJECT` | 3 |  |
| `ANCHOR_KIND_TARGET` | 4 |  |
| `ANCHOR_KIND_NOTE` | 5 |  |

Used by: [GetNote (response)](notes.md#getnote), [ListNotes (response)](notes.md#listnotes).

### AnchorStatus

AnchorStatus is what checking one anchor against the workspace found.

DRIFTED is the case an existence check cannot catch and a reader cannot see: the anchored code still exists and quietly stopped meaning what the note says. BODY\_CHANGED is that same check graded one step finer - the content moved, the DECLARATION did not - and it is a far weaker signal: measured across 1,496 repositories, an edit that leaves the signature alone is 39-105x less likely to come with a prose update than one that changes it, which is why an undifferentiated content hash reports drift that mostly is not there. Surface it, never gate on it. UNVERIFIED is a real, distinct answer and must never be rendered as "fine" - it means no fingerprint was recorded (the note predates fingerprinting, or was never re-read) or none could be computed here, and reporting drift from missing data is the false positive that trains a reader to ignore every flag.

Source: [notes.proto:76](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L76).

| Value | # | Description |
|-------|---|-------------|
| `ANCHOR_STATUS_UNSPECIFIED` | 0 |  |
| `ANCHOR_STATUS_RESOLVES` | 1 |  |
| `ANCHOR_STATUS_DANGLING` | 2 |  |
| `ANCHOR_STATUS_DRIFTED` | 3 |  |
| `ANCHOR_STATUS_UNVERIFIED` | 4 |  |
| `ANCHOR_STATUS_BODY_CHANGED` | 5 |  |

Used by: [GetNote (response)](notes.md#getnote), [ListNotes (response)](notes.md#listnotes).

### Scope

Scope is which store a note lives in, and it is the axis a reader must never have to guess: the two mean different things about who can see the note.

Source: [notes.proto:43](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L43).

| Value | # | Description |
|-------|---|-------------|
| `SCOPE_UNSPECIFIED` | 0 |  |
| `SCOPE_SHARED` | 1 | SHARED is the store inside the checkout: committed, reviewed, attributed by git. |
| `SCOPE_PRIVATE` | 2 | PRIVATE is this machine only. It is never exported to the shared cache and never served on the LAN share listener. |

Used by: [GetNote (response)](notes.md#getnote), [ListNotes (response)](notes.md#listnotes).

### Staleness

Staleness is whether the prose was outrun by the thing it describes.

The signal is NOT calendar age: prose about a subsystem nobody has touched is current, and decaying it by age is how a signal earns the right to be ignored. What is measured is DIVERGENCE between two commit dates. UNMEASURED is not "fresh" - it is the honest answer when VCS history is unavailable, when the prose has no history, or when there is no subject to compare against. Every private note is UNMEASURED today, because staleness is keyed by workspace-relative path and a private note's source is absolute.

Source: [notes.proto:93](https://github.com/egladman/magus/blob/main/proto/magus/notes/v1alpha1/notes.proto#L93).

| Value | # | Description |
|-------|---|-------------|
| `STALENESS_UNSPECIFIED` | 0 |  |
| `STALENESS_UNMEASURED` | 1 |  |
| `STALENESS_CURRENT` | 2 |  |
| `STALENESS_OUTRUN` | 3 |  |
| `STALENESS_PETRIFIED` | 4 |  |

Used by: [GetNote (response)](notes.md#getnote), [ListNotes (response)](notes.md#listnotes).

