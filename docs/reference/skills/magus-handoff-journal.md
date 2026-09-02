---
title: magus-handoff-journal
generated_from: internal/agent/skills/magus-handoff-journal/SKILL.md
description: "Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, pointers, and the hypotheses an investigation ruled out, all surviving worktrees and sessions."
tags: [agents, skills, magus-handoff-journal]
aliases:
  - reference/skills/magus-memory
skill_full_bytes: 5669
skill_simple_bytes: 4662
---

# magus-handoff-journal

Maintain a user-owned handoff journal through magus_memory or `magus memory`: named decisions, plans, pointers, and the hypotheses an investigation ruled out, all surviving worktrees and sessions. Use when a debugging session eliminates a possibility a later session would otherwise re-propose. It is not automatic agent memory; add an entry only when a later person needs to reopen the linked graph/query/output/doc evidence. Verify malformed, stale, broken-linked, and unresolvable-evidence entries before relying on them.

Install it, rather than copying from this page:

```sh
magus agent install .claude/skills   # writes both forms below
```

An installed copy carries a provenance stamp, so `magus doctor` can tell you when a magus upgrade has made it stale. Text copied from this page carries none.

## What an installed copy carries

`magus agent install` writes this frontmatter above the body. `magus doctor` reads it to report whether your installed skills are current.

| field | value |
| --- | --- |
| `license` | `GPL-3.0-or-later` |
| `compatibility` | `any-agent` |
| `source` | `magus` |
| `agent-skill-version` | `51` |
| `knowledge-schema-version` | `10` |
| `skill-content` | `d6432d24cec1` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both permutations below report it: they go stale together, never one silently, and a change to another skill does not move it.

## Full form

Every mechanical step spelled out, plus the rationale for each. Installed as the `<name>-full` twin: loaded by name rather than always, so a reader who needs the long form can ask for it without every session carrying it.

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
| `elimination` | a hypothesis an investigation killed, the why, and an `excerpt` of the evidence | yes, plus the excerpt |

Every type is ref-anchored; none of them is free prose. A claim that is true about
the code is a `pointer` of kind `query` (fetch it live) or `output`, never stored prose.

Prose a PERSON wrote about the code belongs in the workspace's notes store instead - a
different store with a different rule, read with `magus notes`. Agents read notes and never
write them, so nothing here routes you there: if what you have is a human's judgment rather
than a ref you can anchor, it is theirs to record, not yours.

## Read and write deliberately

- At a handoff or session start, use `magus_memory` `{op: "list"}` or
  `magus memory ls`. Empty is normal; do not manufacture journal entries.
- Use `get` before revisiting a named decision. If evidence has changed, update
  that entry and its status instead of silently contradicting it.
- Use `put` for a decision or plan another person would otherwise have to
  rediscover. It writes only the fields you send, so refreshing a status keeps the
  body and refs beside it. Rely on that rather than re-sending the whole
  record from memory: the store keeps no history, so a field you drop is gone with
  nothing to restore it from. Clearing a field means deleting the entry and creating it again,
  and a record cannot be updated into another type. The CLI is often clearer for a human:

  ```sh
  magus memory put release-gate --type plan \
    --ref 'command: magus affected ci' --status active \
    --body 'Run after the documentation render is committed.'
  magus memory put release-gate --amend --status done
  ```

- Use `delete` for entries that no longer earn their keep. Run `magus memory
  verify` (or MCP `{op: "verify"}`) after editing entries or when list reports
  an issue. It gives a path and repair step for malformed, stale, or broken
  linked entries, and warns when an entry's evidence ref no longer resolves.

## Recording

- `magus_memory` {op: "put", name, type, refs, body?, excerpt?, status?} creates a record
  by `name` (a kebab slug), and on a name that exists writes the fields you send and
  keeps the rest. Pass `refs` as one per line, `kind: target` (e.g.
  `query: kind=op depends cache` or `node: file:internal/hash/hasher.go`); sending
  `refs` replaces the whole list.
- Pass `allow_missing: false` (CLI `--amend`) when you mean to land on an entry that
  already exists, so a mistyped name is an error rather than a second entry
  nobody goes looking for.
- Made a choice another session would otherwise re-derive (architecture, naming,
  a rejected approach and why): record a `decision`. A bare "we chose X" helps
  nobody; the `body` carries the why, and the refs anchor it to the code.
- Ruled a hypothesis OUT: record an `elimination`. `body` says why it is dead and
  `excerpt` carries the lines that killed it, because an output ref resolves
  only from the checkout that minted it and agent worktrees get deleted, which decays a
  ref-only record into a dangling pointer with a confident tone. The ref stays beside the excerpt
  as a best-effort handle. Record what an investigation ELIMINATED as well as what it
  concluded, so the next session reopens the reasoning; a conclusion on its own leaves it
  re-proposing a branch that is already dead.
- Prefer a ref over prose: if a fact is derivable, record the `query` that proves
  it, not a sentence that rots.
- Prune with `op: "delete"`; list-then-get with `op: "list"` / `op: "get"`.

## Scope boundaries

- Intra-session scratch (checklists, partial findings) stays in the
  session - it is disposable by definition, not here.
- Facts the repo already records (code structure, git history, MAGUS.md) do not
  belong in memory; record the `magus_query` that surfaces them instead.
- Records live outside the repo, keyed by repository identity. The console,
  CLI, and MCP all show the same entries. A legacy cursor can still be read for
  migration, but writes are intentionally retired: one shared cursor lets one
  session erase another's handoff.
````

## Short form

The enumeration dropped, the judgment kept - for the most capable readers, not the least; the bar under the heading above shows by how much. This is the always-loaded primary. Both are hand-authored from one source body; see [Skills](../../guides/integrations/agents/skills.md) for the difference.

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
| `elimination` | a hypothesis an investigation killed, the why, and an `excerpt` of the evidence | yes, plus the excerpt |

Every type is ref-anchored; none of them is free prose.
A claim true about the code is a `query` or `output` pointer, never stored prose.

Prose a PERSON wrote about the code belongs in the workspace's notes store instead - a
different store with a different rule, read with `magus notes`. Agents read notes and never
write them, so nothing here routes you there: if what you have is a human's judgment rather
than a ref you can anchor, it is theirs to record, not yours.

## Read and write deliberately

- At a handoff or session start, use `magus_memory` `{op: "list"}` or
  `magus memory ls`.
- Use `get` before revisiting a named decision. If evidence has changed, update
  that entry and its status instead of silently contradicting it.
- Use `put` for a decision or plan another person would otherwise have to
  rediscover. It writes only the fields you send, so refreshing a status keeps the
  body and refs beside it, and a dropped field has no history to restore
  it from. Clearing a field means deleting the entry and creating it again,
  and a record cannot be updated into another type.

  ```sh
  magus memory put release-gate --type plan \
    --ref 'command: magus affected ci' --status active \
    --body 'Run after the documentation render is committed.'
  magus memory put release-gate --amend --status done
  ```

- Use `delete` for entries that no longer earn their keep. Run `magus memory
  verify` (or MCP `{op: "verify"}`) after editing entries or when list reports
  an issue.

## Recording

- `magus_memory` {op: "put", name, type, refs, body?, excerpt?, status?} creates a record
  by `name` (a kebab slug), and on a name that exists writes the fields you send and
  keeps the rest. Pass `refs` as one per line, `kind: target` (e.g.
  `query: kind=op depends cache` or `node: file:internal/hash/hasher.go`); sending
  `refs` replaces the whole list.
- Pass `allow_missing: false` (CLI `--amend`) when you mean to land on an entry that
  already exists, so a mistyped name is an error.
- Made a choice another session would otherwise re-derive (architecture, naming,
  a rejected approach and why): record a `decision`. Put the why
  in `body` and anchor it with refs.
- Ruled a hypothesis OUT: record an `elimination`. `body` says why it is dead and
  `excerpt` carries the lines that killed it, because an
  output ref dies with the checkout that minted it. The ref stays beside the excerpt
  as a best-effort handle. Record what an investigation ELIMINATED as well as what it
  concluded, so the next session reopens the reasoning; a conclusion on its own leaves it
  re-proposing a branch that is already dead.
- Prefer a ref over prose: if a fact is derivable, record the `query` that proves
  it.
- Prune with `op: "delete"`; list-then-get with `op: "list"` / `op: "get"`.

## Scope boundaries

- Intra-session scratch (checklists, partial findings) stays in the
  session, not here.
- Facts the repo already records (code structure, git history, MAGUS.md) do not
  belong in memory; record the `magus_query` that surfaces them instead.
- Records live outside the repo, keyed by repository identity. Console, CLI and MCP all show the same entries.
````


</details>
