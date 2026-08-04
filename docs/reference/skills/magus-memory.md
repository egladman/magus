---
title: magus-memory
description: "Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, and pointers that survive worktrees and sessions."
tags: [agents, skills, magus-memory]
skill_full_bytes: 3843
skill_simple_bytes: 3165
---

# magus-memory

Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, and pointers that survive worktrees and sessions. It is not automatic agent memory; add an entry only when a later person needs to reopen the linked graph/query/output/doc evidence. Verify malformed, stale, and broken-linked entries before relying on them.

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills            # the full form below
magus agent install .claude/skills --simple   # the short form below
```

An installed copy carries a provenance stamp, so `magus graph verify` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus graph verify` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `24` |
| `knowledge-schema-version` | `7` |
| `skill-content` | `dda9599a90ae` |
| `skill-variant` | `full` |

The `skill-content` digest is shared by both permutations below, so they version together: a magus upgrade makes both stale at once, never one silently.

## Full form

The default: the steps plus the rationale for each.

````markdown
# Handoff journal

`magus memory` and `magus_memory` are two frontends to a small, user-owned
handoff journal. It lives outside the repo, is shared by its worktrees, and is
visible in the console. It is not automatic model memory: add an entry only
when a person or a later session needs a named decision, plan, or saved lens.

The graph remains the source of truth. A journal entry links back to the query,
node, output, command, or document that a later reader should reopen.

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

## Read and write deliberately

- At a handoff or session start, use `magus_memory` `{op: "list"}` or
  `magus memory ls`. Empty is normal; do not manufacture journal entries.
- Use `get` before revisiting a named decision. If evidence has changed, update
  that entry and its status instead of silently contradicting it.
- Use `put` for a decision or plan another person would otherwise have to
  rediscover. The CLI is often clearer for a human:

  ```sh
  magus memory put release-gate --type plan \
    --ref 'command: magus affected ci' --status active \
    --body 'Run after the documentation render is committed.'
  ```

- Use `delete` for entries that no longer earn their keep. Run `magus memory
  verify` (or MCP `{op: "verify"}`) after editing entries or when list reports
  an issue. It gives a path and repair step for malformed, stale, or broken
  linked entries.

## Recording

- `magus_memory` {op: "put", name, type, refs, body?, status?} upserts a record
  by `name` (a kebab slug). Pass `refs` as one per line, `kind: target` (e.g.
  `query: kind:op depends cache` or `node: file:internal/hash/hasher.go`).
- Made a choice another session would otherwise re-derive (architecture, naming,
  a rejected approach and why): record a `decision`. A bare "we chose X" helps
  nobody; the `body` carries the why, and the refs anchor it to the code.
- Prefer a ref over prose: if a fact is derivable, record the `query` that proves
  it, not a sentence that rots.
- Prune with `op: "delete"`; list-then-get with `op: "list"` / `op: "get"`.

## Scope boundaries

- Intra-session working notes (checklists, partial findings) belong in
  `magus_scratchpad`, which is per-workspace and disposable, not here.
- Facts the repo already records (code structure, git history, MAGUS.md) do not
  belong in memory; record the `magus_query` that surfaces them instead.
- Records live outside the repo, keyed by repository identity. The console,
  CLI, and MCP all show the same entries. A legacy cursor can still be read for
  migration, but writes are intentionally retired: one shared cursor lets one
  session erase another's handoff.
````

## Short form (`--simple`)

The same steps with the rationale withheld; the bar under the heading above shows by how much. Both are hand-authored from one source body; see [Agents](../../guides/integrations/agents.md) for when to prefer which.

<details>
<summary>Show the short form</summary>

````markdown
# Handoff journal

`magus memory` and `magus_memory` are two frontends to a small, user-owned
handoff journal. It is not automatic model memory: add an entry only
when a person or a later session needs a named decision, plan, or saved lens.

The graph remains the source of truth.

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

There is no free-text/`note` type. A claim true
about the code is a `query` or `output` pointer, never stored prose.

## Read and write deliberately

- At a handoff or session start, use `magus_memory` `{op: "list"}` or
  `magus memory ls`.
- Use `get` before revisiting a named decision. If evidence has changed, update
  that entry and its status instead of silently contradicting it.
- Use `put` for a decision or plan another person would otherwise have to
  rediscover.

  ```sh
  magus memory put release-gate --type plan \
    --ref 'command: magus affected ci' --status active \
    --body 'Run after the documentation render is committed.'
  ```

- Use `delete` for entries that no longer earn their keep. Run `magus memory
  verify` (or MCP `{op: "verify"}`) after editing entries or when list reports
  an issue.

## Recording

- `magus_memory` {op: "put", name, type, refs, body?, status?} upserts a record
  by `name` (a kebab slug). Pass `refs` as one per line, `kind: target` (e.g.
  `query: kind:op depends cache` or `node: file:internal/hash/hasher.go`).
- Made a choice another session would otherwise re-derive (architecture, naming,
  a rejected approach and why): record a `decision`. Put the why
  in `body` and anchor it with refs.
- Prefer a ref over prose: if a fact is derivable, record the `query` that proves
  it.
- Prune with `op: "delete"`; list-then-get with `op: "list"` / `op: "get"`.

## Scope boundaries

- Intra-session working notes (checklists, partial findings) belong in
  `magus_scratchpad`, not here.
- Facts the repo already records (code structure, git history, MAGUS.md) do not
  belong in memory; record the `magus_query` that surfaces them instead.
- Records live outside the repo, keyed by repository identity. Console, CLI and MCP all show the same entries.
````


</details>
