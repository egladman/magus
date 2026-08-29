# Auditing the instructions an agent was given

This is a LENS, like `magus_insight`: it observes and ranks, it does not gate.
The output is a findings list a human decides on, never an automatic edit.

What it looks at is not code. It is everything loaded into an agent's context as
authoritative instruction, from sources magus does not own and cannot see the
contents of in advance.

{{if .Full}}An agent reads all of it in one window, with no way to tell which line is newer,
which file outranks which, or which was written for a version of the tools that
no longer exists. A contradiction between any two is not a documentation nit: the
model cannot resolve it from inside, so it picks one arbitrarily, alternates
across turns, or spends reasoning budget arbitrating instead of working.{{else}}An agent reads all of it in one window and cannot tell which line is newer or
which file wins. A contradiction makes it pick arbitrarily or stall.{{end}}

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

{{if .Full}}The user-level file is the one to check first precisely because it is not in the
repo: nothing about the workspace hints that it exists, and its author may not be
the person hitting the contradiction.{{end}}

## Check claims against the TOOL, not against the other documents

{{if .Full}}Two documents agreeing with each other and both being wrong is the common case,
not the exception - they were usually written in the same sitting by the same
person.{{end}} So resolve every claim against something that executes.

```sh
printf '%s' "<the exact command a document recommends>" | magus session hook
magus describe targets -o name        # does the target a doc names still exist
magus describe file <path>            # is that file really source / output
```

Then RUN the commands the instructions tell an agent to run.{{if .Full}} A documented command
that errors is worse than an undocumented one: the agent trusts it, tries it,
fails, and has to invent a recovery nothing sanctioned.{{end}}

Work outward from what CHANGED - a diff, a changelog, a handoff - rather than
reading everything. Contradictions cluster around recent edits.

```sh
grep -rn "<the command or rule>" <every surface you enumerated>
```

## Rank what you find

Report findings in this order.{{if .Full}} Severity here is "how badly does this derail a
session", not "how wrong is the sentence".{{end}}

1. **Dead end** - A forbids X, B requires X, and no third path exists. The agent
   must either violate a rule or stall.{{if .Full}} Nothing else on this list is worth
   reporting before one of these.{{end}}
2. **Stale instruction** - a named command no longer exists, no longer works, or
   is now denied.{{if .Full}} Indistinguishable from a dead end until the agent tries it.{{end}}
3. **Split authority** - two surfaces describe the same decision differently
   (one "advised", the other "denied"). The agent cannot tell which is current.
   {{if .Full}}A workspace-local rule contradicting a shipped skill is always this finding:
   local text overrides nothing, so the two are simply in conflict.{{else}}A local rule contradicting a shipped skill is always this.{{end}} Check each
   local rule's `retire-when` while you are here; the condition may have arrived.
4. **Orphaned replacement** - a denial or deprecation names a tool that no
   instruction anywhere documents.
5. **Silent duplication** - the same rule restated in several places.{{if .Full}} Not yet a
   contradiction; it is where the next one is born, because an edit will update
   some of them.{{end}}

## Do not report these

{{if .Full}}A lens that cries wolf gets switched off, taking the real findings with it.{{end}}

- **Different altitudes.** A skill giving the full ladder and a hook injection
  giving one line are one rule at two lengths. That is the design.
- **A record of history.** "Verified on <date> by doing X" describes what
  happened, not what to do now. Journal entries are point-in-time by definition.
- **A stated exception.** "Never pipe output, EXCEPT <case>" reads as a conflict
  on a grep and is one rule with a carve-out.
- **A labeled migration note** describing old behavior on purpose.

## Recommend, then verify the fix landed

Fix at the SOURCE and let generation propagate{{if .Full}}; editing an installed copy is
drift a verify step will flag anyway{{end}}. For magus's own skills that means
`internal/agent/skills/*/SKILL.md`, then reinstall.

Reinstall with a binary built from the EDITED source, and confirm the content
digest moved:

```sh
magus doctor    # the agent skills check must report a CHANGED digest, or the install did nothing
```

A stale binary re-installs the OLD body and reports success{{if .Full}}, which is the single
most common way an applied fix silently does not apply{{end}}.

Prefer DELETING a contradicting line over reconciling it.{{if .Full}} Two reconciled
statements are still two statements to keep in sync; one statement cannot
contradict itself.{{end}} When a rule genuinely must appear twice, make one the source
and have the other name it rather than restate it.
