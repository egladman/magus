---
title: magus-context-audit
generated_from: internal/agent/skills/magus-context-audit/SKILL.md
description: "Audit the instructions an agent was given - the repo instruction file, installed skills, handoff-journal entries, a routing index, hook-injected text, and any user-level instruction file - for statements that contradict each other or that no longer match what the tools do."
tags: [agents, skills, magus-context-audit]
skill_full_bytes: 5846
skill_simple_bytes: 4187
---

# magus-context-audit

Audit the instructions an agent was given - the repo instruction file, installed skills, handoff-journal entries, a routing index, hook-injected text, and any user-level instruction file - for statements that contradict each other or that no longer match what the tools do. Use after changing a guard rule, a denied command, or a documented workflow; before shipping a change to the agent surface; and when an agent has been behaving inconsistently or ignoring a rule. This is a lens over INSTRUCTIONS, not over code: it reports ranked findings for a human to act on and never edits anything itself.

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
| `agent-skill-version` | `48` |
| `knowledge-schema-version` | `10` |
| `skill-content` | `883a29c81e8d` |
| `skill-variant` | `full` |

The `skill-content` digest covers this skill alone, and both permutations below report it: they go stale together, never one silently, and a change to another skill does not move it.

## Full form

Every mechanical step spelled out, plus the rationale for each. Installed as the `<name>-full` twin: loaded by name rather than always, so a reader who needs the long form can ask for it without every session carrying it.

````markdown
# Auditing the instructions an agent was given

This is a LENS, like `magus_insight`: it observes and ranks, it does not gate.
The output is a findings list a human decides on, never an automatic edit.

What it looks at is not code. It is everything loaded into an agent's context as
authoritative instruction, from sources magus does not own and cannot see the
contents of in advance.

An agent reads all of it in one window, with no way to tell which line is newer,
which file outranks which, or which was written for a version of the tools that
no longer exists. A contradiction between any two is not a documentation nit: the
model cannot resolve it from inside, so it picks one arbitrarily, alternates
across turns, or spends reasoning budget arbitrating instead of working.

## Enumerate before reading

You cannot audit what you cannot list, and the highest-risk surface is usually
the one nobody remembers is loaded.

| surface | why it bites |
| --- | --- |
| the repo's agent instruction file (`CLAUDE.md`, `AGENTS.md`, ...) | always loaded, whole file, never scoped |
| installed skills | whole directory; a stale one looks identical to a current one |
| a local, workspace-owned skill (`magus-local-development`) | loads beside the shipped set, but nothing generates or verifies it |
| the handoff journal / memory entries | loaded at session start, and POINT-IN-TIME by definition |
| a routing index (`MAGUS.md`) | invites being read, only true as of its last regeneration |
| hook-injected text | fires on every matching tool call, and nothing displays it in one place |
| a user-level or global instruction file | invisible from inside the repo, and outranks nothing |
| tool output the agent is told to trust | deny reasons, usage text, doctor hints |

The user-level file is the one to check first precisely because it is not in the
repo: nothing about the workspace hints that it exists, and its author may not be
the person hitting the contradiction.

## Check claims against the TOOL, not against the other documents

Two documents agreeing with each other and both being wrong is the common case,
not the exception - they were usually written in the same sitting by the same
person. So resolve every claim against something that executes.

```sh
printf '%s' "<the exact command a document recommends>" | magus session hook
magus describe targets -o name        # does the target a doc names still exist
magus describe file <path>            # is that file really source / output
```

Then RUN the commands the instructions tell an agent to run. A documented command
that errors is worse than an undocumented one: the agent trusts it, tries it,
fails, and has to invent a recovery nothing sanctioned.

Work outward from what CHANGED - a diff, a changelog, a handoff - rather than
reading everything. Contradictions cluster around recent edits.

```sh
grep -rn "<the command or rule>" <every surface you enumerated>
```

## Rank what you find

Every finding carries the command that REPRODUCES it - the exact line a reader
runs to see the contradiction for themselves, not a description of where you saw
it. A finding nobody can re-run is an opinion about a document, and it gets
argued with instead of fixed.

Report findings in this order. Severity here is "how badly does this derail a
session", not "how wrong is the sentence".

1. **Dead end** - A forbids X, B requires X, and no third path exists. The agent
   must either violate a rule or stall. Nothing else on this list is worth
   reporting before one of these.
2. **Stale instruction** - a named command no longer exists, no longer works, or
   is now denied. Indistinguishable from a dead end until the agent tries it.
3. **Split authority** - two surfaces describe the same decision differently
   (one "advised", the other "denied"). The agent cannot tell which is current.
   A workspace-local rule contradicting a shipped skill is always this finding:
   local text overrides nothing, so the two are simply in conflict. Check each
   local rule's `retire-when` while you are here; the condition may have arrived.
4. **Orphaned replacement** - a denial or deprecation names a tool that no
   instruction anywhere documents.
5. **Silent duplication** - the same rule restated in several places. Not yet a
   contradiction; it is where the next one is born, because an edit will update
   some of them.

## Do not report these

A lens that cries wolf gets switched off, taking the real findings with it.

- **Different altitudes.** A skill giving the full ladder and a hook injection
  giving one line are one rule at two lengths. That is the design.
- **A record of history.** "Verified on <date> by doing X" describes what
  happened, not what to do now. Journal entries are point-in-time by definition.
- **A stated exception.** "Never pipe output, EXCEPT <case>" reads as a conflict
  on a grep and is one rule with a carve-out.
- **A labeled migration note** describing old behavior on purpose.

## Recommend, then verify the fix landed

Fix at the SOURCE and let generation propagate; editing an installed copy is
drift a verify step will flag anyway. For magus's own skills that means
`internal/agent/skills/*/SKILL.md`, then reinstall.

Reinstall with a binary built from the EDITED source, and confirm the content
digest moved:

```sh
magus doctor    # the agent skills check must report a CHANGED digest, or the install did nothing
```

A stale binary re-installs the OLD body and reports success, which is the single
most common way an applied fix silently does not apply.

Prefer DELETING a contradicting line over reconciling it. Two reconciled
statements are still two statements to keep in sync; one statement cannot
contradict itself. When a rule genuinely must appear twice, make one the source
and have the other name it rather than restate it.
````

## Short form

The enumeration dropped, the judgment kept - for the most capable readers, not the least; the bar under the heading above shows by how much. This is the always-loaded primary. Both are hand-authored from one source body; see [Skills](../../guides/integrations/agents/skills.md) for the difference.

<details>
<summary>Show the short form</summary>

````markdown
# Auditing the instructions an agent was given

This is a LENS, like `magus_insight`: it observes and ranks, it does not gate.
The output is a findings list a human decides on, never an automatic edit.

What it looks at is not code. It is everything loaded into an agent's context as
authoritative instruction, from sources magus does not own and cannot see the
contents of in advance.

An agent reads all of it in one window and cannot tell which line is newer or
which file wins. A contradiction makes it pick arbitrarily or stall.

## Enumerate before reading

You cannot audit what you cannot list, and the highest-risk surface is usually
the one nobody remembers is loaded.

| surface | why it bites |
| --- | --- |
| the repo's agent instruction file (`CLAUDE.md`, `AGENTS.md`, ...) | always loaded, whole file, never scoped |
| installed skills | whole directory; a stale one looks identical to a current one |
| a local, workspace-owned skill (`magus-local-development`) | loads beside the shipped set, but nothing generates or verifies it |
| the handoff journal / memory entries | loaded at session start, and POINT-IN-TIME by definition |
| a routing index (`MAGUS.md`) | invites being read, only true as of its last regeneration |
| hook-injected text | fires on every matching tool call, and nothing displays it in one place |
| a user-level or global instruction file | invisible from inside the repo, and outranks nothing |
| tool output the agent is told to trust | deny reasons, usage text, doctor hints |

## Check claims against the TOOL, not against the other documents

 So resolve every claim against something that executes.

```sh
printf '%s' "<the exact command a document recommends>" | magus session hook
magus describe targets -o name        # does the target a doc names still exist
magus describe file <path>            # is that file really source / output
```

Then RUN the commands the instructions tell an agent to run.

Work outward from what CHANGED - a diff, a changelog, a handoff - rather than
reading everything. Contradictions cluster around recent edits.

```sh
grep -rn "<the command or rule>" <every surface you enumerated>
```

## Rank what you find

Every finding carries the command that REPRODUCES it - a finding nobody can re-run is an opinion about a
document.

Report findings in this order.

1. **Dead end** - A forbids X, B requires X, and no third path exists. The agent
   must either violate a rule or stall.
2. **Stale instruction** - a named command no longer exists, no longer works, or
   is now denied.
3. **Split authority** - two surfaces describe the same decision differently
   (one "advised", the other "denied"). The agent cannot tell which is current.
   A local rule contradicting a shipped skill is always this. Check each
   local rule's `retire-when` while you are here; the condition may have arrived.
4. **Orphaned replacement** - a denial or deprecation names a tool that no
   instruction anywhere documents.
5. **Silent duplication** - the same rule restated in several places.

## Do not report these

- **Different altitudes.** A skill giving the full ladder and a hook injection
  giving one line are one rule at two lengths. That is the design.
- **A record of history.** "Verified on <date> by doing X" describes what
  happened, not what to do now. Journal entries are point-in-time by definition.
- **A stated exception.** "Never pipe output, EXCEPT <case>" reads as a conflict
  on a grep and is one rule with a carve-out.
- **A labeled migration note** describing old behavior on purpose.

## Recommend, then verify the fix landed

Fix at the SOURCE and let generation propagate. For magus's own skills that means
`internal/agent/skills/*/SKILL.md`, then reinstall.

Reinstall with a binary built from the EDITED source, and confirm the content
digest moved:

```sh
magus doctor    # the agent skills check must report a CHANGED digest, or the install did nothing
```

A stale binary re-installs the OLD body and reports success.

Prefer DELETING a contradicting line over reconciling it. When a rule genuinely must appear twice, make one the source
and have the other name it rather than restate it.
````


</details>
