---
title: "ADR 0036: a subagent's structured input and output are records it files, judged at spawn and stop"
order: 36
description: A subagent's input is the job row it is spawned under, and its output is the JobResult it files with magus job exit. The workspace's own Buzz rules on magus\guard.spawn and magus\guard.stop refuse a spawn or a stop that lacks the record, and the shape stays the workspace's choice. magus rewrites neither the brief nor the reply, though Claude Code, Codex, Cursor and OpenCode would each let a hook rewrite one or both.
tags: [adr, decision, agents, harness, guard, jobs, subagents]
status: proposed
date: 2026-11-08
---

# ADR 0036: a subagent's structured input and output are records it files, judged at spawn and stop

## Context

Eli asked on 2026-11-08 for structured input and output for subagents. A hook can see
the text a subagent is spawned with and the text it returns, and on most hosts it can
replace that text. The question is whether magus should use that to force a shape, with
the shape left open to configuration.

Read on 2026-11-08 from this tree at e555cb60a and from each vendor's hook reference
(Claude Code `code.claude.com/docs/en/hooks`, Codex `learn.chatgpt.com/docs/hooks`,
Cursor `cursor.com/docs/agent/hooks`, OpenCode `opencode.ai/docs/build/plugins/server`).

### What each host lets a hook see and change

At spawn time:

| host        | spawn event magus wires                                            | brief the hook reads                               | can a hook rewrite it                                                                                 | can a hook refuse it            |
| ----------- | ------------------------------------------------------------------ | -------------------------------------------------- | ----------------------------------------------------------------------------------------------------- | ------------------------------- |
| Claude Code | `PreToolUse` on `Agent`, `Task`                                    | `tool_input.prompt`, `description`, `model`        | yes: `updatedInput` replaces the whole input; with `allow` it also skips the permission prompt        | yes: `permissionDecision: deny` |
| Codex       | `PreToolUse` on `spawn_agent`, `collaborationspawn_agent`, `Agent` | v1 `message` or `items`; v2 `message` is encrypted | v1 only: `updatedInput` is honored only beside `permissionDecision: allow`; a v2 brief cannot be read | yes                             |
| Cursor      | `preToolUse` on `Task`                                             | `prompt`, `description`, `subagent_type`           | documented: `updated_input`; not probed on `Task`                                                     | yes: `permission: deny`         |
| OpenCode    | `tool.execute.before` on `task`                                    | `prompt`, `description`, `subagent_type`           | yes: the plugin is handed `output.args` by reference                                                  | yes: the plugin throws          |

Codex `SubagentStart` adds `additionalContext` to the child, and magus already sends the
checkout brief there. Claude Code's `SubagentStart` adds context too, but carries no
prompt. Cursor's `subagentStart` can deny, but it never fires in cursor-agent
([Cursor](../guides/integrations/agents/cursor.md)).

At return time:

| host        | event that sees the reply                                      | reply the hook reads                   | can a hook rewrite what the parent reads                                        | can a hook send the child back                                                       |
| ----------- | -------------------------------------------------------------- | -------------------------------------- | ------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| Claude Code | `SubagentStop`; `PostToolUse` on `Agent`, `Task`               | `last_assistant_message`               | yes: `PostToolUse` `updatedToolOutput`, for any tool whose output shape matches | yes: `SubagentStop` `decision: block` with a `reason` keeps the child running        |
| Codex       | `SubagentStop`; `PostToolUse` on the spawn                     | `last_assistant_message` (may be null) | partly: a `PostToolUse` block replaces the tool result with the hook's feedback | yes: `SubagentStop` `decision: block` continues the child                            |
| Cursor      | none fires: no `postToolUse` for `Task`, `subagentStop` unseen | none                                   | no                                                                              | not expressible: `subagentStop` `followup_message` exists and never fired in a probe |
| OpenCode    | `tool.execute.after` on `task`                                 | the tool's `output`                    | yes: the plugin is handed the output object by reference                        | no hook; only a new prompt into the child session, which is acting, not judging      |

### What magus has today

- `magus\guard.spawn` hands a workspace rule a `SpawnRequest` (`types/spawn.go`) with the
  brief as `prompt`, the `description`, the `model` and the caller's `lease`, and takes
  back allow, advise or deny ([guard.spawn](../reference/guard-spawn.md)). This
  repository's rule is `judgeSpawn` in `hack/policy/guard.buzz`.
- `magus\guard.stop` hands a rule a `StopRequest` (`types/guard.go:288`) with the
  `event` (`Stop` or `SubagentStop`), `stopHookActive`, the `transcript` path, the
  `lease` and the checkout state. It carries no reply text. This repository's rule is
  `judgeStop`, set in `magusfile.buzz:23`
  ([ADR 0019](0019-a-mandate-carries-out-decisions-already-made.md#6-the-stop-rule)).
- The spawn is recorded as an `agent_spawn` event with the brief as a blob, joined to a
  job when the description reads `<parent>/<role> <job>` or the brief opens with
  `lease: <id>` ([leases](../guides/integrations/agents/leases.md#the-spawn-is-recorded-never-judged)).
- The job row is the structured input already: write paths, check, criteria and model,
  forked with `magus job fork` and validated before a spawn may name it
  ([spawn-unvalidated](../reference/rules/spawn-unvalidated.md)).
- `magus job exit --stdin` files a `JobResult` against the schema `magus job exit
  --schema` prints (generated from `types.JobResult` by `magus-utils jobschema`):
  `changed_paths`, `validation`, `verification_evidence`, `unresolved_risks`,
  `follow_ups`. The record is mirrored into Buzz as `JobResult`, and `Job.result` carries
  it (`internal/spell/gen/decls/magus.buzz:821`, `:935`), so a rule reading `req.lease`
  can see whether one was filed.
- Every hook reply says it carries no `updatedInput`
  ([Claude Code](../guides/integrations/agents/claude-code.md), [Codex](../guides/integrations/agents/codex.md)).

### The principle in play

[The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
magus judges and informs, and never rewrites the command, the prompt or the reply on the
way through. The blog post of 2026-09-14 ("I didn't set out to build a harness") adds the
other force: an agent is good at writing structured data and worse at reading it. That
favors a typed output an agent writes over a typed input it must parse.

## Decision

### 1. The input is the job row, not a reshaped brief

A subagent's structured input is the job row it is spawned under. The spawn's brief stays
the parent's prose, and nothing rewrites it. The row is already typed, validated before
the spawn, and rendered for the child by `magus describe job` and the checkout brief.
Nothing is built for this part.

### 2. The output is the JobResult the child files

A subagent's structured output is the `JobResult` it files with `magus job exit --stdin`.
The parent reads it with `magus job wait` or `magus describe job`, from any checkout, on
every host, whatever the child's last message says. The reply text stays free and is not
rewritten.

### 3. The workspace's Buzz rule requires the record

The shape is configured where away mode is: in the workspace's own Buzz policy.

- A `magus\guard.spawn` rule may refuse a spawn with no job row, or one whose row lacks
  what the workspace wants (a check, criteria, a model). The request already carries
  `lease` and `description`.
- A `magus\guard.stop` rule may refuse a leased subagent's `SubagentStop` while
  `req.lease.result` is null, naming `magus job exit <job> --stdin` and the schema
  command. On Claude Code and Codex the deny becomes `decision: block`, so the child
  keeps running and files the record itself.

magus ships no such rule, the same stance as `magus\guard.spawn`.

### 4. One generic host addition: the reply on StopRequest

`StopRequest` gains `reply`: the stopping agent's last message as the host reported it,
empty when it reported none. Claude Code and Codex carry it as `last_assistant_message`.
Cursor's `subagentStop` documents `summary` but never fired. OpenCode has no stop event
for a child. This field lets a workspace rule judge an unleased subagent's reply against
any shape it declares: parse it with `json`, compare it with a buzzgen-generated object,
and deny with the reason. magus does not parse the reply itself.

| part                                                           | where                                                 | State     |
| -------------------------------------------------------------- | ----------------------------------------------------- | --------- |
| job row as the structured input                                | `magus job fork`, `spawn-unvalidated`                 | done      |
| `JobResult` as the structured output                           | `magus job exit --stdin`, `--schema`                  | done      |
| `Job.result` readable from a stop rule's `req.lease`           | `types.Job`, Buzz `Job.result`                        | done      |
| `StopRequest.reply` from `last_assistant_message`              | `types/guard.go`, the Claude Code and Codex stop glue | proposed  |
| a reference stop rule that requires a filed result             | `hack/policy/guard.buzz` `judgeStop`                  | proposed  |
| a reference spawn rule that requires a job row for every spawn | `hack/policy/spawn.buzz`                              | proposed  |
| rewriting a brief or a reply in a hook                         | none                                                  | not built |

### Principles checked

- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  every enforcement point is a deny with a reason. The child or the parent changes its
  own text; no hook reply carries `updatedInput`, `updatedToolOutput` or a rewritten
  result.
- [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md):
  the stop rule refuses rather than advises, because a result nobody is required to file
  is a result nobody files.
- [One source, every door](../design/principles/one-source-every-door.md): the output
  schema is `types.JobResult`, which already generates the JSON schema and the Buzz
  object. No second schema is written.
- [The host wiring is yours](../design/judgment/the-host-wiring-is-yours.md): the shape
  lives in the workspace's Buzz, versioned with the code it governs; magus adds one
  field a host already reports.
- [No silent defaults](../design/judgment/no-silent-defaults.md): `reply` is empty when
  the host reports none, and the host pages name which hosts do.

## Alternatives

### Rewrite the brief at spawn time

Wrap the parent's prompt in a template (the job row, a required output section) through
`updatedInput` or OpenCode's `output.args`. It lost on the principle: the child would read
a brief the parent never wrote and the person never reviewed, and the transcript would
not say so. It also does not reach every host: a Codex v2 brief is encrypted, and Codex
honors `updatedInput` only beside `allow`, which turns every guard pass into an approval
that skips Codex's own prompt. Reopen if a host offers an append-only channel the
transcript records as the hook's, which Codex `SubagentStart` `additionalContext` already
is, and which magus already uses for state, not shape.

### Rewrite the reply at return time

Parse the child's last message and replace the parent's view with a normalized record
through `updatedToolOutput` or OpenCode's mutable output. It lost for the same reason,
and on reach: Cursor has no return event at all, and Codex can only replace the result
with feedback text. A reply that fails to parse would also need a fallback, which is a
silent default. Reopen never on these grounds; the filed record already reaches the
parent on every host.

### A new subagent I/O schema

Declare a dedicated input and output schema for subagents. It lost to the records already
present: the job row and `JobResult` carry what a parent acts on, have generators, a
validator and a Buzz mirror, and `magus job wait` already grades against them. Reopen if
a workspace needs a field on a result that `JobResult` cannot carry; the answer then is a
field on `JobResult`.

### Ship the rule in magus

Make "a leased worker must file a result before it stops" a built-in rule. It lost
because whether a read-only scout must file one is a workspace's call (`magus job exit`
already ends a read-only row with nothing to grade as pass). Reopen if every workspace
that adopts it writes the same rule.

### Send the child back from OpenCode's idle event

OpenCode has no stop hook for a child session. The plugin could prompt the child session
again when it goes idle without a result. It lost because that is magus acting, not
judging. On OpenCode the `tool.execute.after` reply can still tell the parent, which then
continues the child itself.

## Consequences

- On Claude Code and Codex a leased child cannot stop without filing a `JobResult`, once a
  workspace writes the stop rule. The child does the filing; magus refuses until it does.
- On Cursor and OpenCode the child's stop is not seen, so the rule cannot hold it. The
  parent's `magus job wait` still finds no filed result and does not pass the row, so
  the gap shows later rather than not at all.
- The brief and the reply the person reads in the transcript are the ones the models
  wrote.
- An unleased subagent's reply can be held to a workspace-declared shape on Claude Code
  and Codex through `reply`. Nowhere else.

## Open questions

- Whether `reply` should also be offered on `magus\guard.spawn`'s continue request, where
  `SendMessage` carries the parent's text but not the child's.
- Whether Cursor's `subagentStop` fires in the IDE, where the CLI probe saw nothing. A
  probe there would move Cursor's return row from not expressible to judged.
- Whether OpenCode's V2 `execute.after` exposes the child session id the way V1's
  `metadata.sessionId` does; the V2 page does not say.
- The number is free on main at this writing (main ends at 0032; 0034 and 0035 are on open
  branches). Recheck at merge.

## Amendments
