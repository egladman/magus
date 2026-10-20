{{if .Full}}# Auditing the instructions an agent was given{{else}}# Auditing agent instructions{{end}}

{{if .Full}}This is a LENS, like `{{buzz "insight"}}`: it observes and ranks; it does not gate.
The output is a findings list a human decides on, never an automatic edit.

It looks at the text, not code: everything an agent loads as
authoritative, from sources magus does not own and cannot read in advance.{{else}}Lens, like `{{buzz "insight"}}`: observes, ranks; does not gate.
Output: findings list human decides on, never automatic edit.
Looks at text, not code: everything agent loads as authoritative, from sources magus does not own and cannot read in advance.
Agent reads all of it in one window; cannot tell which line newer or which file wins.
When two lines disagree -> agent picks one arbitrarily or stalls.{{end}}{{if .Full}}

An agent reads all of it in one window, with no way to tell which line is newer,
which file outranks which, or which was written for a version of the tools that
no longer exists. When two lines disagree, that is not a documentation nit: the
model cannot resolve it from inside, so it picks one arbitrarily, alternates
across turns, or spends reasoning budget arbitrating instead of working.{{end}}

## Enumerate before reading

{{if .Full}}You cannot audit what you cannot list. The riskiest source is usually the one
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
| tool output the agent is told to trust | deny reasons, usage text, doctor hints |{{else}}Cannot audit what you cannot list; riskiest source: one nobody remembers loading.
- repo agent file (`CLAUDE.md`, `AGENTS.md`, ...): always loaded, whole file, never scoped
- installed skills: whole directory; stale one looks identical to current
- local workspace-owned skill (`magus-local-development`): loads beside shipped set; nothing generates or verifies it
- harness memory entries: loaded at session start; point-in-time by nature
- routing index (`MAGUS.md`): invites reading; only true up to last regeneration
- hook-injected text: fires on every matching tool call; nothing displays it in one place
- user-level or global instruction file: invisible from inside repo; outranks nothing
- tool output agent trusts: deny reasons, usage text, doctor hints{{end}}{{if .Full}}

The user-level file is the one to check first precisely because it is not in the
repo: nothing about the workspace hints that it exists, and its author may not be
the person hitting the conflict.{{end}}

{{if .Full}}## Check claims against the tool, not against the other documents{{else}}## Check claims against tools, not documents{{end}}

{{if .Full}}Two documents agreeing with each other and both being wrong is the common case,
not the exception: they were usually written in the same sitting by the same
person. Resolve every claim against something that executes.{{else}}Resolve every claim against something that executes.{{end}}

```sh
magus shell "<the exact command a document recommends>"
magus describe targets -o name        # does the target a doc names still exist
magus describe file <path>            # is that file really source / output
```
{{if .Full}}
Then RUN the commands the instructions tell an agent to run. A documented command
that errors is worse than an undocumented one: the agent trusts it, tries it,
fails, and has to invent a recovery nothing sanctioned.
{{end}}
{{if .Full}}Work outward from what CHANGED (a diff, a changelog, a recent decision), not by
reading everything. Conflicting lines cluster around recent edits.{{else}}Then run commands instructions tell agent to run.
Work outward from what changed (diff, changelog, recent decision), not reading everything; conflicts cluster around recent edits.{{end}}

```sh
grep -rn "<the command or rule>" <every source you enumerated>
```

{{if .Full}}## Rank what you find{{else}}## Rank{{end}}
{{if .Full}}
Every finding carries the command that REPRODUCES it: the exact line a reader
runs to see the conflict for themselves, not a description of where you saw
it. A finding nobody can re-run is an opinion about a document, and it gets
argued with instead of fixed.{{end}}{{if .Full}}

Report findings in this order. Here the order measures "how badly does this derail a
session", not "how wrong is the sentence".
{{end}}
{{if .Full}}1. Dead end: A forbids X, B requires X, and no third path exists. The agent must
   break one of the two or stall. Nothing else on this list is worth
   reporting before one of these.{{else}}Every finding carries command that reproduces it; finding nobody can re-run is opinion.
Order:
1. Dead end: A forbids X, B requires X, no third path. Agent must break one or stall.{{end}}
{{if .Full}}2. Stale claim: it names a command that no longer exists, no longer works,
   or is now denied. It is indistinguishable from a dead end until the agent tries it.{{else}}2. Stale claim: names command that no longer exists, no longer works, or now denied.{{end}}
{{if .Full}}3. Split verdict: two sources describe one decision differently (one
   "advised", the other "denied"), and the agent cannot tell which is current.
   A workspace-local statement contradicting a shipped skill is always this finding:
   local text overrides nothing, so the two are in conflict. Check each
   local statement's `retire-when` while here; the retire-when may already be true.{{else}}3. Split verdict: two sources describe one decision differently (one "advised", other "denied"); agent cannot tell which current. Local statement contradicting shipped skill: always this. Check each local statement's `retire-when` while here; retire-when may already be true.{{end}}
{{if .Full}}4. Orphaned successor: a denial or a deprecated command names a tool no statement
   documents.{{else}}4. Orphaned successor: denial or deprecated command names tool no statement documents.{{end}}
{{if .Full}}5. Silent copies: one statement restated in several places. It is not yet a
   conflict; it is where the next one is born, because an edit updates
   some of them.{{else}}5. Silent copies: one statement restated in several places.{{end}}

{{if .Full}}## Do not report these{{else}}## Do not report{{end}}
{{if .Full}}
A lens that cries wolf gets switched off, taking the real findings with it.
{{end}}
{{if .Full}}- **Different altitudes.** A skill giving the full ladder and a hook injection
  giving one line are one statement at two lengths. That is the design.{{else}}- Different altitudes: skill with full ladder, hook injection with one line: one statement, two lengths; by design.{{end}}
{{if .Full}}- **A record of history.** "Verified on <date> by doing X" describes what happened,
  not what to do now. Journal entries are point-in-time by nature.{{else}}- Record of history: "Verified on <date> by doing X" describes what happened, not what to do now; journal entries point-in-time by nature.{{end}}
{{if .Full}}- **A stated exception.** "Never pipe output, EXCEPT <case>" looks like a conflict
  to grep and is one statement with a carve-out.{{else}}- Stated exception: "Never pipe output, EXCEPT <case>" looks like conflict to grep; one statement with carve-out.{{end}}
{{if .Full}}- **A labeled note** that describes old behavior on purpose.{{else}}- Labeled note describing old behavior on purpose.{{end}}

{{if .Full}}## Recommend, then verify the fix landed{{else}}## Recommend, then verify fix landed{{end}}

{{if .Full}}Fix at the SOURCE and let the generator propagate it; editing an installed copy is
drift a verify step flags anyway. For magus's own skills that is
`internal/agent/skills/*/SKILL.md`, then reinstall.

Reinstall with a binary built from the EDITED source, and confirm the content
digest moved:{{else}}Fix at source; generator propagates.
magus's own skills: `internal/agent/skills/*/SKILL.md`, then reinstall.
Reinstall with binary built from edited source; confirm content digest moved:{{end}}

```sh
magus doctor    # the agent skills check must report a CHANGED digest, or the install did nothing
```

{{if .Full}}A stale binary re-installs the OLD body and reports success, which is the single
most common way an applied fix silently does not apply.

Prefer DELETING a conflicting line over reconciling it. Two reconciled
statements are still two statements to keep in sync; one statement cannot
contradict itself. When a statement must appear twice, make one the source and have
the other name it, not restate it.{{else}}Stale binary re-installs old body, reports success.
Prefer deleting conflicting line over reconciling.
When statement must appear twice -> one is source, other names it, not restates it.{{end}}
