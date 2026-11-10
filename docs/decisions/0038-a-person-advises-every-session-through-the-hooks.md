---
title: "ADR 0038: a person advises every session at once, through the hooks magus already installs"
order: 38
description: magus session advise appends a person's directive to one JSON Lines store per repository. The guard hands each open directive to every root session once, as that person's own words, at the next user prompt where the host takes context and at every host's stop. Delivery and, when the person names one, the command that would carry it out are recorded per session, so magus session advisories says how many sessions saw it and how many acted. No provider messaging tool is involved.
tags: [adr, decision, agents, harness, guard, sessions, human-in-the-loop]
status: accepted
date: 2026-11-09
---

# ADR 0038: a person advises every session at once, through the hooks magus already installs

## Context

On 2026-11-07 Eli changed plans for about eight running sessions at once. The root
session messaged each one by hand. A child session heard only through its parent's
relay, and one refused to push because a relayed message "doesn't count" as Eli's OK.
The harness shortcomings log carries the case under that date.

magus already carries the other direction. `magus session notify` opens an attention
request and `magus session attention` lists it for a person
(`internal/sessions/attention.go`). Nothing carries a person's words to the agents.

The constraints:

- magus stays out of the providers' way. A design built on a host's messaging tool
  (SendMessage and the like) would need a magus release each time a provider changes one.
- Every harness this repository declares (`spells/harness/{claude-code,codex,cursor,opencode}`)
  already wires a stop hook into `magus shell`: the `stop` hook point. Claude Code and
  Codex also send each user prompt to `magus shell --message`
  (`docs/guides/integrations/agents/magus-command.buzz`, `recordMessage`), which today
  records it and answers nothing.
- A stop reply that denies is a `decision: block` on Claude Code and Codex, a
  `followup_message` on Cursor (`cursor-hook.buzz`, `gateStop`) and a follow-up prompt on
  OpenCode (`spells/harness/opencode/spell.buzz`, the `session.idle` row). Each puts text
  in front of the model and keeps it working.
- A Claude Code or Codex `UserPromptSubmit` reply may carry
  `hookSpecificOutput.additionalContext`. Cursor's `beforeSubmitPrompt` can only let the
  message through, and the OpenCode spell says no OpenCode event hands text to the model
  once a turn.
- [The record of refusals](../design/refusals.md) deleted the `code-search` and
  `doc-search` advisories at 0.9% uptake over 6,075 servings and 2.5% over 812
  (measured 2026-09-11). A text the guard serves is not acted on because it was served.
- The session store holds 25 invocation files (100 KB) for this repository on
  2026-11-09. They are pruned at retention, and an open attention request pins the files
  that name it ([`Prune`](../../internal/sessions/rotate.go)).

## Decision

### 1. One store per repository

| Item                                                                                                                                              | State |
| ------------------------------------------------------------------------------------------------------------------------------------------------- | ----- |
| `<XDG state>/magus/advisories/<repo>-<hash12>/advisories.jsonl`, keyed through `vcs.StateDir` as the session store is, so every worktree reads it | done  |
| One entry type, `sessions.directiveEntry`, with its own `MarshalJSON` and `UnmarshalJSON`; `kind` is `post`, `retract`, `delivered` or `acted`    | done  |
| The last line per key wins: a post and its retraction key on the advisory id, a delivery or an action on the id and the host session              | done  |
| The text is clamped to `sessions.MaxMessageBytes`, the bound attention messages already carry                                                     | done  |

The id is `adv-` plus 12 hex digits of the sha256 of the text and the post time, so two
posts of one text are two advisories.

### 2. Three verbs, all a person's

| Item                                                                                                                                               | State |
| -------------------------------------------------------------------------------------------------------------------------------------------------- | ----- |
| `magus session advise "<text>" [--expect <command prefix>]` posts one advisory and prints its id                                                   | done  |
| `magus session advisories [--all]` lists open advisories, oldest first, with how many sessions each reached and acted; `--all` adds retracted ones | done  |
| `magus session retract <id>` closes one by id or unambiguous prefix, as `session dispose` does                                                     | done  |
| The guard denies `session advise` and `session retract` to an agent, beside `session dispose` and `diff --ack`                                     | done  |

Nothing closes an advisory but `retract`: no expiry and no count of deliveries. A
session started after the post sees it too, because the person's word stands until the
person takes it back.

### 3. Delivery through the guard

| Item                                                                                                                                                                                                                                                                                                         | State                               |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------- |
| At a root `Stop`, a judgment that does not already deny becomes a deny whose reason is the advisory, on every host: the session keeps working and reads it                                                                                                                                                   | done                                |
| At a user prompt, `magus shell --message --renders-context` answers with the advisory as `context`, and `magus-command.buzz` renders it as `UserPromptSubmit` `additionalContext` (Claude Code, Codex); a wiring without `--renders-context` is shown nothing and marked nothing, so its stop still shows it | done                                |
| Each advisory reaches one host session once: the guard appends `delivered` before it answers, and a session with a delivery for an id is never shown it again                                                                                                                                                | done                                |
| A subagent event (`SubagentStop`, or any call that names an agent id) delivers nothing, so a child cannot spend its root's one showing                                                                                                                                                                       | done                                |
| The text opens with who posted it (the OS user `magus session advise` ran as), when, and the id, and says it is that person's own instruction to every session, not a relay                                                                                                                                  | done                                |
| Cursor's stop reply drops its `[magus guard]` prefix for an advisory, which is a person's words, not the guard's                                                                                                                                                                                             | done                                |
| Cursor and OpenCode prompt delivery                                                                                                                                                                                                                                                                          | not built (no channel; see Context) |

An idle session sees the advisory on its next prompt or stop. Waking one is the host's job.

### 4. Measuring whether sessions act

| Item                                                                                                                                                                                                                                                                 | State |
| -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----- |
| `delivered` per session is the reach: `session advisories` prints how many host sessions were shown each advisory                                                                                                                                                    | done  |
| `--expect <prefix>` names what acting on it looks like (`--expect "git push"`); the guard appends `acted` the first time a session it delivered to runs a shell command starting with that prefix, counting a subagent's command for its root and never a denied one | done  |
| `session advisories` prints `acted M of N`, or `acted: not measured (no --expect)`, never a bare count                                                                                                                                                               | done  |

An advisory without `--expect` is measured for reach only, and the listing says so. The
guard checks the prefix only on shell commands of a session holding a delivery whose
advisory is open and unacted, so the cost is one read of a small file per judged call.

### Principles checked

- [Human in the loop](../design/principles/human-in-the-loop.md): the person's
  instruction reaches every session first-hand, which is the gap the incident showed.
- [Agents propose, humans dispose](../design/judgment/agents-propose-humans-dispose.md)
  and [Manual on purpose](../design/judgment/manual-on-purpose.md): only a person posts or
  retracts, the guard enforces it, and nothing expires on its own.
- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  magus adds context and blocks a stop, the two replies its hooks already give. It never
  rewrites the person's prompt or the model's reply.
- [Everything is a file](../design/principles/everything-is-a-file.md): one append-only
  JSON Lines file, encoding on the entry type.
- [Measured, not asserted](../design/principles/measured-not-asserted.md): reach and
  action are recorded per session from the start, so the feature can be judged by the
  same kind of number that deleted the search advisories.
- [No silent defaults](../design/judgment/no-silent-defaults.md): an unmeasured
  advisory says it is unmeasured, and a store the guard cannot read is named in the
  judgment's context instead of reading as "no advisories".

## Alternatives

### Send through each host's messaging tool

Lost: it binds magus to SendMessage-shaped tools that differ per provider and change
between releases, the lockstep Eli ruled out. It also reaches only sessions the sender
can address. Reopen if a cross-provider messaging standard ships that magus can call
without per-host code.

### Keep relaying through the parent session

Lost: it is what failed on 2026-11-07. A relayed text is the parent's words, and a child
that refuses it is right to. Evidence: the harness shortcomings entry of that date.
Nothing reopens it.

### Store advisories as facts in the session store

Lost: the session store is one file per invocation, pruned at retention. An advisory and
its delivery marks must outlive the invocation that posted them, which would add a pin
rule to `Prune` beside the attention one. The guard reads advisories on every prompt and
stop, and one small file is cheaper than folding a directory. Reopen if the session store
moves to one file per repository.

### Show it on every prompt until the agent acknowledges it

Lost: an agent can acknowledge without acting, so an ack measures nothing that delivery
does not, and repeating a text is how the search advisories reached 6,075 servings.
Reopen if delivered-once sessions show they missed it, such as a delivery followed by a
compaction that dropped it.

### Prepend the text to the person's prompt

Lost: it rewrites what the person typed, which the outer harness principle forbids, and
no host hook offers it uniformly. Nothing reopens it.

### Expire an advisory after a duration (`--for 2h`)

Lost for now: an expiry closes the person's instruction without the person, against
manual-on-purpose. Reopen if open advisories pile up in practice (Open questions).

### Wake idle sessions

Lost: starting a turn is acting, and magus computes and refuses; it does not act. The
host app owns waking a session.

## Consequences

- One command reaches every root session in every worktree of the repository, on every
  declared harness, at its next stop, and on Claude Code and Codex at its next prompt.
- A session that was stopping keeps working for one more turn with the advisory in front
  of it. That is the point for "push what you have", and a cost for an advisory that asks
  for nothing.
- An agent that writes `advisories.jsonl` directly, outside the repository, can still
  forge a post. The guard denies the verb, not the file. The OS user it records is the
  same either way.
- `magus shell --message` now answers instead of printing nothing, so a host wiring that
  discards its stdout still records the message but shows no advisory at the prompt; the
  stop still shows it.

## Open questions

- What `acted M of N` rate keeps the feature. The search advisories were deleted below
  3%; a person's directive should do far better. Read it after the first ten advisories
  posted with `--expect`.
- Whether advisories need a per-worktree or per-lease audience. Every session in the
  repository is the only audience built.
- Whether open advisories accumulate enough to need an expiry.

## Amendments
