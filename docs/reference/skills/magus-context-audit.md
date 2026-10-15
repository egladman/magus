---
title: magus-context-audit
generated_from: internal/agent/skills/magus-context-audit/SKILL.md
description: "Audit the instructions an agent was given for statements that contradict each other or no longer match the tools."
tags: [agents, skills, magus-context-audit]
skill_full_bytes: 5888
skill_short_bytes: 4063
---

# magus-context-audit

Audit the instructions an agent was given for statements that contradict each other or no longer match the tools. Covers the repo instruction file, installed skills, memory, the routing index, hook text and user-level files. Use after changing a guard rule, a denied command, or a documented workflow; before shipping a change to the agent integration; and when an agent has been behaving inconsistently or ignoring a rule. This is a lens over INSTRUCTIONS, not over code: it reports ranked findings for a human to act on and never edits anything itself.

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
| `agent-skill-version` | `130` |
| `knowledge-schema-version` | `21` |
| `skill-content` | `310dbdcd81b6` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both forms below report it: they go stale together, never one silently, and a change to another skill does not move it.

## The two forms

Both are hand-authored from one source body. The short form is the always-loaded primary - the enumeration dropped, the judgment kept, for the most capable readers rather than the least. The full form is its `<name>-full` twin, loaded by name when a reader wants the rationale. The bar above shows how much shorter the primary is; switch between them here to see exactly what it gave up. See [Skills](../../guides/integrations/agents/skills.md) for how to choose.

<article class="landing-tabs">
<header>
<input type="radio" name="magus-context-audit-variant" id="magus-context-audit-tab-short" checked>
<label for="magus-context-audit-tab-short">Short form</label>
<input type="radio" name="magus-context-audit-variant" id="magus-context-audit-tab-full">
<label for="magus-context-audit-tab-full">Full form</label>
</header>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-context-audit/SKILL.md
```

````markdown
# Auditing the instructions an agent was given

This is a LENS, like `magus\insight`: it observes and ranks; it does not gate.
The output is a findings list a human decides on, never an automatic edit.

It looks at the text, not code: everything an agent loads as
authoritative, from sources magus does not own and cannot read in advance.

An agent reads all of it in one window and cannot tell which line is newer or
which file wins. When two lines disagree, it picks one arbitrarily or stalls.

## Enumerate before reading

You cannot audit what you cannot list. The riskiest source is usually the one
nobody remembers is loaded.

| source | why it bites |
| --- | --- |
| the repo's agent file (`CLAUDE.md`, `AGENTS.md`, ...) | always loaded, whole file, never scoped |
| installed skills | whole directory; a stale one looks identical to a current one |
| a local, workspace-owned skill (`magus-local-development`) | loads beside the shipped set, but nothing generates or verifies it |
| the harness's own memory entries | loaded at session start, and POINT-IN-TIME by nature |
| a routing index (`MAGUS.md`) | invites being read, only true up to the last time it was regenerated |
| hook-injected text | fires on every matching tool call, and nothing displays it in one place |
| a user-level or global instruction file | invisible from inside the repo, and outranks nothing |
| tool output the agent is told to trust | deny reasons, usage text, doctor hints |

## Check claims against the tool, not against the other documents

 Resolve every claim against something that executes.

```sh
magus shell "<the exact command a document recommends>"
magus describe targets -o name        # does the target a doc names still exist
magus describe file <path>            # is that file really source / output
```

Then RUN the commands the instructions tell an agent to run.

Work outward from what CHANGED (a diff, a changelog, a recent decision), not by
reading everything. Conflicting lines cluster around recent edits.

```sh
grep -rn "<the command or rule>" <every source you enumerated>
```

## Rank what you find

Every finding carries the command that REPRODUCES it: a finding nobody can re-run is an opinion about a
document.

Report findings in this order.

1. Dead end: A forbids X, B requires X, and no third path exists. The agent must
   break one of the two or stall.
2. Stale claim: it names a command that no longer exists, no longer works,
   or is now denied.
3. Split verdict: two sources describe one decision differently (one
   "advised", the other "denied"), and the agent cannot tell which is current.
   A local statement contradicting a shipped skill is always this. Check each
   local statement's `retire-when` while here; the retire-when may already be true.
4. Orphaned successor: a denial or a deprecated command names a tool no statement
   documents.
5. Silent copies: one statement restated in several places.

## Do not report these

- **Different altitudes.** A skill giving the full ladder and a hook injection
  giving one line are one statement at two lengths. That is the design.
- **A record of history.** "Verified on <date> by doing X" describes what happened,
  not what to do now. Journal entries are point-in-time by nature.
- **A stated exception.** "Never pipe output, EXCEPT <case>" looks like a conflict
  to grep and is one statement with a carve-out.
- **A labeled note** that describes old behavior on purpose.

## Recommend, then verify the fix landed

Fix at the SOURCE and let the generator propagate it. For magus's own skills that is
`internal/agent/skills/*/SKILL.md`, then reinstall.

Reinstall with a binary built from the EDITED source, and confirm the content
digest moved:

```sh
magus doctor    # the agent skills check must report a CHANGED digest, or the install did nothing
```

A stale binary re-installs the OLD body and reports success.

Prefer DELETING a conflicting line over reconciling it. When a statement must appear twice, make one the source and have
the other name it, not restate it.
````


</section>

<section class="landing-tabpanel">

```sh
magus agent install --tar | tar -xO -f - magus-context-audit-full/SKILL.md
```

````markdown
# Auditing the instructions an agent was given

This is a LENS, like `magus\insight`: it observes and ranks; it does not gate.
The output is a findings list a human decides on, never an automatic edit.

It looks at the text, not code: everything an agent loads as
authoritative, from sources magus does not own and cannot read in advance.

An agent reads all of it in one window, with no way to tell which line is newer,
which file outranks which, or which was written for a version of the tools that
no longer exists. When two lines disagree, that is not a documentation nit: the
model cannot resolve it from inside, so it picks one arbitrarily, alternates
across turns, or spends reasoning budget arbitrating instead of working.

## Contents

- Enumerate before reading
- Check claims against the tool, not against the other documents
- Rank what you find
- Do not report these
- Recommend, then verify the fix landed

## Enumerate before reading

You cannot audit what you cannot list. The riskiest source is usually the one
nobody remembers is loaded.

| source | why it bites |
| --- | --- |
| the repo's agent file (`CLAUDE.md`, `AGENTS.md`, ...) | always loaded, whole file, never scoped |
| installed skills | whole directory; a stale one looks identical to a current one |
| a local, workspace-owned skill (`magus-local-development`) | loads beside the shipped set, but nothing generates or verifies it |
| the harness's own memory entries | loaded at session start, and POINT-IN-TIME by nature |
| a routing index (`MAGUS.md`) | invites being read, only true up to the last time it was regenerated |
| hook-injected text | fires on every matching tool call, and nothing displays it in one place |
| a user-level or global instruction file | invisible from inside the repo, and outranks nothing |
| tool output the agent is told to trust | deny reasons, usage text, doctor hints |

The user-level file is the one to check first precisely because it is not in the
repo: nothing about the workspace hints that it exists, and its author may not be
the person hitting the conflict.

## Check claims against the tool, not against the other documents

Two documents agreeing with each other and both being wrong is the common case,
not the exception: they were usually written in the same sitting by the same
person. Resolve every claim against something that executes.

```sh
magus shell "<the exact command a document recommends>"
magus describe targets -o name        # does the target a doc names still exist
magus describe file <path>            # is that file really source / output
```

Then RUN the commands the instructions tell an agent to run. A documented command
that errors is worse than an undocumented one: the agent trusts it, tries it,
fails, and has to invent a recovery nothing sanctioned.

Work outward from what CHANGED (a diff, a changelog, a recent decision), not by
reading everything. Conflicting lines cluster around recent edits.

```sh
grep -rn "<the command or rule>" <every source you enumerated>
```

## Rank what you find

Every finding carries the command that REPRODUCES it: the exact line a reader
runs to see the conflict for themselves, not a description of where you saw
it. A finding nobody can re-run is an opinion about a document, and it gets
argued with instead of fixed.

Report findings in this order. Here the order measures "how badly does this derail a
session", not "how wrong is the sentence".

1. Dead end: A forbids X, B requires X, and no third path exists. The agent must
   break one of the two or stall. Nothing else on this list is worth
   reporting before one of these.
2. Stale claim: it names a command that no longer exists, no longer works,
   or is now denied. It is indistinguishable from a dead end until the agent tries it.
3. Split verdict: two sources describe one decision differently (one
   "advised", the other "denied"), and the agent cannot tell which is current.
   A workspace-local statement contradicting a shipped skill is always this finding:
   local text overrides nothing, so the two are in conflict. Check each
   local statement's `retire-when` while here; the retire-when may already be true.
4. Orphaned successor: a denial or a deprecated command names a tool no statement
   documents.
5. Silent copies: one statement restated in several places. It is not yet a
   conflict; it is where the next one is born, because an edit updates
   some of them.

## Do not report these

A lens that cries wolf gets switched off, taking the real findings with it.

- **Different altitudes.** A skill giving the full ladder and a hook injection
  giving one line are one statement at two lengths. That is the design.
- **A record of history.** "Verified on <date> by doing X" describes what happened,
  not what to do now. Journal entries are point-in-time by nature.
- **A stated exception.** "Never pipe output, EXCEPT <case>" looks like a conflict
  to grep and is one statement with a carve-out.
- **A labeled note** that describes old behavior on purpose.

## Recommend, then verify the fix landed

Fix at the SOURCE and let the generator propagate it; editing an installed copy is
drift a verify step flags anyway. For magus's own skills that is
`internal/agent/skills/*/SKILL.md`, then reinstall.

Reinstall with a binary built from the EDITED source, and confirm the content
digest moved:

```sh
magus doctor    # the agent skills check must report a CHANGED digest, or the install did nothing
```

A stale binary re-installs the OLD body and reports success, which is the single
most common way an applied fix silently does not apply.

Prefer DELETING a conflicting line over reconciling it. Two reconciled
statements are still two statements to keep in sync; one statement cannot
contradict itself. When a statement must appear twice, make one the source and have
the other name it, not restate it.
````


</section>

</article>
