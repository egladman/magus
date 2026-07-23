---
name: magus-memory
description: Keep durable, cross-session project memory in a magus workspace via the magus_memory tool: discrete, categorized RECORDS that persist outside the repo and survive model, session, and agent-host changes. Each record is a typed POINTER into the magus domain (a saved query, a graph node, an output ref, a command, a doc), not free prose; only a decision/plan carries a short why. Use at the start of any session (op=list to ramp), and record a decision the moment one is made. Requires a running magus daemon (MCP).
---

# Durable memory across sessions and models

The session ends; the next one may be a different model, a different agent host,
or weeks later. `magus_memory` keeps a set of durable RECORDS per repository in
the user's state directory. They live outside the repo (never committed, never
another contributor's problem) and are shared across every git worktree and
branch. What a stronger model decided, a smaller model can read and apply.

## What a memory is (and is not)

A memory is a typed POINTER into the magus domain, not a free-text note. The
payload is one or more `refs`; the ref IS the memory. If you cannot name a ref
kind for something, it is not a memory. It is a query you should just run.

The graph holds the truth (what the code IS); memory holds the curation: which
query, node, output, or doc mattered, and, for a decision, the why the graph
cannot derive.

Ref kinds (the closed set a ref may point at):

| kind      | points at                              | resolves via              |
| --------- | -------------------------------------- | ------------------------- |
| `query`   | a saved `magus query` expression       | re-run it                 |
| `node`    | a graph node id (`target:...`, `file:...`) | `magus query <id>`    |
| `output`  | a target output ref id (`out1a2b3c`)   | `magus query <ref>`       |
| `command` | a magus invocation to reproduce something | run it                 |
| `doc`     | a docs anchor                          | open the doc              |

Record types (the subject axis):

| type       | payload                                             | prose? |
| ---------- | --------------------------------------------------- | ------ |
| `pointer`  | refs only, the saved lens onto graphed knowledge    | no     |
| `decision` | a choice, its refs, and the WHY the graph can't derive | yes (a one-line caption) |
| `plan`     | forward intent, its refs, and the why               | yes    |

There is no free-text/`note` type. A claim that is true about the code is a
`pointer` of kind `query` (fetch it live) or `output`, never stored prose.

## Session start

1. `magus_memory` {op: "list"} returns what is already recorded. Ramp on it. Do not
   re-litigate a decision recorded here; if new evidence contradicts one, say so
   explicitly and record the reversal (update the record's `status`).
2. `magus_memory` {op: "cursor"} returns where the last session left off.

Empty results just mean a fresh project; start recording.

## Recording

- `magus_memory` {op: "put", name, type, refs, body?, status?} upserts a record
  by `name` (a kebab slug). Pass `refs` as one per line, `kind: target` (e.g.
  `query: kind:op depends cache` or `node: file:internal/hash/hasher.go`).
- Made a choice another session would otherwise re-derive (architecture, naming,
  a rejected approach and why): record a `decision`. A bare "we chose X" helps
  nobody; the `body` carries the why, and the refs anchor it to the code.
- Prefer a ref over prose: if a fact is derivable, record the `query` that proves
  it, not a sentence that rots.
- Before ending a session: `magus_memory` {op: "cursor", content: "..."} so the
  next session's first read is current. A stale cursor is worse than none.
- Prune with `op: "delete"`; list-then-get with `op: "list"` / `op: "get"`.

## Scope boundaries

- Intra-session working notes (checklists, partial findings) belong in
  `magus_scratchpad`, which is per-workspace and disposable, not here.
- Facts the repo already records (code structure, git history, MAGUS.md) do not
  belong in memory; record the `magus_query` that surfaces them instead.
- Records live outside the repo, keyed by repository identity; the tool result
  includes the fields when a human wants to read or edit them via the console.
