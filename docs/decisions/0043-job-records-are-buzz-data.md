---
title: "ADR 0043: job records are Buzz data, and Markdown is rendered from them"
order: 43
description: What a parent hands a child is the job row, and what a child hands back is the JobResult it files; both are Go types mirrored into Buzz by the one generator, and the only prose is rendered from them by one Buzz template, never written first and parsed later. The word brief goes; the parent's record is the job spec, and the rendered Markdown is a view of it. The guard holds a spawn to a complete, validated row and a leased child's stop to a filed result. agents.brief_format, magus\Brief, the buzz prompt format and StopRequest.reply are deleted as a deliberate break, and the change is judged against the spawn baseline collected before it.
tags: [adr, decision, agents, jobs, guard, buzz, subagents, harness]
status: proposed
date: 2026-11-10
supersedes: ADR 0036 sections 3 and 4 (the workspace-only stop rule and StopRequest.reply), and agents.brief_format
---

# ADR 0043: job records are Buzz data, and Markdown is rendered from them

## Context

Eli, 2026-11-10, on the subagent input and output work: "I'm a fan of the brief format.
I also just don't like the word brief. I feel like this feature could be more fleshed out
and be more explicit. I don't like all these agentic changes; I would almost argue it makes
it more of an agentic tool. We want to speak in favor of actual, through Buzz, always, and
then you can output Markdown from that, from the raw data. Structured data first, and then
it goes into whatever you want it to be. Structured data can be turned into Markdown, not
the other way around. This feature needs to be rethought."

The same day, on the questions this record's draft left him: the parent's record is the
"spec"; on shipping `stop-unfiled` as a built-in rule, "yes"; and on the render path, "view,
as in job view / job-view.buzz: says it's a rendering of the data, never stored".

Read on 2026-11-10 from origin/main at a0625c0fe:

- [ADR 0036](0036-subagent-structured-io.md) (proposed) decides the input is the job row
  and the output is the `JobResult` filed with `magus job exit --stdin`. Its stop rule that
  requires a filed result is left to the workspace and not built; `StopRequest.reply` is
  proposed and not built.
- `agents.brief_format` takes `markdown` (the default) or `buzz` (`types/brief.go`,
  `internal/config/agents.go:29`, and a file since deleted, see the 2026-11-12 amendment).
  A `buzz` prompt is one
  `magus\Brief{job, goal, why, decisions, evidence}` literal, which `judgeBriefBuzz` in
  `internal/guard/brief.go:97` type-checks against the generated declarations. Nothing
  rewrites it and nothing reads it after the check: the literal is text in the prompt and
  never reaches the store.
- The render-brief.buzz script (since replaced by `hack/dev/show-job.buzz`) rendered
  Markdown for a worker from `magus describe job -o json` and `magus ls jobs -o json`, parsed as `any`, through a `Terms` object it declares
  itself (`render-brief.buzz:50`). It refuses a row with no check, or no write paths on a
  job that is not read-only (`:271`).
- `magus describe job <id>` prints the row for its holder, and `magus job exec` ends with
  "read the terms with `./magus describe job <id>`".
- Structured results carry `next` steps (`types.NextStep`, `cmd/magus/next.go`), the
  commands a reader runs next, as data beside the record.
- This repository sets no `brief_format` (`magus.yaml:199` carries `agents.models` only),
  so the `buzz` format has run nowhere, and nothing measures any of it. A separate scout is
  collecting the baseline now: spawns, spawns whose title names a row, results filed, refs
  filed, tokens and reply bytes per child.
- The word brief carries three senses in the tree: the prompt a parent spawns with
  (`brief-command`, `brief-plain-commands`, 222 files), a skill load (`spawn-unbriefed`,
  `buzz-unbriefed`, `architecture-unbriefed`) and a summary (`magus session --brief`).

The forces: [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md)
forbids rewriting the prompt or the reply; [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md)
says a record nobody is required to file is a record nobody files; and
[The model proposes, the harness executes](../design/principles/the-model-proposes-the-harness-executes.md)
keeps magus computing and refusing, never acting.

## Decision

### 1. Two records, both Go types mirrored into Buzz

A parent's record is the job spec: the declared fields of the job row, `types.Job`, mirrored
as `magus\Job`. A child's record is `types.JobResult`, mirrored as `magus\JobResult`. There
is no third record. Both mirrors come
from the one generator of [ADR 0035](0035-buzz-bindings-come-from-one-config-driven-generator.md)
and are read in Buzz through the typed repository of
[ADR 0030](0030-buzz-reads-state-through-typed-repositories.md), never through
`magus\cmd(... -o json)` parsed as `any`.

The row gains the two fields `magus\Brief` carried that the row did not:

| field                | type              | what it holds                                                                              |
| -------------------- | ----------------- | ------------------------------------------------------------------------------------------ |
| `Job.Decisions`      | `[]JobDecision`   | the choices already made, `what` and `why`, so the holder does not reopen them             |
| `Job.Evidence`       | `[]JobEvidence`   | pointers the holder can open: `kind` (output, verdict, invocation, file, plan, commit), `id`, `says` |

`Criteria` stays the one prose field a parent writes, and the Goals stay the half magus
grades. The spawn prompt is the parent's own words and the reply is the child's own words;
neither is a record, neither is parsed and neither is rewritten (ADR 0036 sections 1 and 2
stand). A parent that wants the child to read something puts it on the row; the prompt
names the row, as `<parent>/<role> <job>` already does.

### 2. One render path, Buzz template over the record, markdown out

The rendered Markdown is a view: a reading of the record for a person or a host that wants
prose, never a record itself. It is rendered from the two records by one Buzz script,
`hack/dev/show-job.buzz`, which replaces render-brief.buzz in the same directory. It
reads `magus\Job` and `magus\JobResult` typed, renders through the `template` host module,
and returns `result\Result::<str>`. It has two entry points: a spec's view, for a worker, a
Codex `SubagentStart` context or a mod pane; and a result's view, for a person or a pull
request. The script keeps the repository's
own steps (the bootstrap script to run, the skill to load, "run nothing else that
validates"), which is why it is repository Buzz and not Go: core never names a `hack/`
script ([The host wiring is yours](../design/judgment/the-host-wiring-is-yours.md)).

Rendered Markdown is never stored and never read back. `magus describe job` stays the
terminal door onto the same row and parses nothing. Core ships the records, `-o json`,
`-o template=` and the `template` module; it ships no Markdown.

No `magus job view <id>` verb ships. The verb would have to run `show-job.buzz` under `hack/dev`,
and core never names a repository's Buzz script path
([The host wiring is yours](../design/judgment/the-host-wiring-is-yours.md); the stop and
spawn rules reach the guard only because the magusfile registers them). In this repository
the view is `magus buzz hack/dev/show-job.buzz -- --job <id>`, one command a person, a
worker or a host can run. A workspace that wants one door for it declares the renderer in
its magusfile, as it declares its guard rules, and a verb that runs the declared renderer
and refuses by name when none is declared is the only shape such a verb could take. That
declaration is not built here: no caller has asked for the door, and a verb with nothing
declared behind it is a silent default waiting to happen.

### 3. The names

| was                                                    | is                                                         | why                                                                                                                                                                                                                                                 |
| ------------------------------------------------------ | ---------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| a brief (the text handed to a worker)                  | the job spec                                               | data first: a spec is the declared half of a record, which is what a parent writes and the store validates, where a brief is a message that is read. The prior art is Kubernetes' `Job.spec`, which the row already leans on (`Generation` as `metadata.generation`, `MaxAttempts` as `backoffLimit`) and which Eli already uses for version names. Eli picked it, 2026-11-10 |
| the Markdown rendered from a record                    | a view                                                     | a view is read off data and never written back, which is the one direction this record allows. Eli, 2026-11-10: "view, as in job view / job-view.buzz: says it's a rendering of the data, never stored"                                               |
| `magus\Brief`, `BriefDecision`, `BriefRef`, `BriefRefKind` | fields on `Job`: `JobDecision`, `JobEvidence`, `JobEvidenceKind` | a second record beside the row said the same things twice, once in the store and once in a prompt the store never saw                                                                                                                      |
| `agents.brief_format`, `Job.BriefFormat`, `types.BriefFormat` | nothing                                               | there is no format to choose: the record has one shape, and the prompt is prose by definition                                                                                                                                                      |
| `brief-command`, `brief-plain-commands`                | `spawn-command`, `spawn-plain-commands`                    | they grade the spawn's prompt, and every other spawn-time rule is `spawn-*`                                                                                                                                                                         |
| `spawn-unbriefed`, `buzz-unbriefed`, `architecture-unbriefed` | `spawn-before-skill`, `buzz-before-skill`, `architecture-before-skill` | each refuses an act before a skill loaded; the new name says what it catches                                                                                                                                                       |
| render-brief.buzz, render-brief-*.json | `show-job.buzz` and `job-view-*.json` in the same directories | named for what it renders                                                                                                                                                                                                                     |
| the checkout brief Codex `SubagentStart` receives      | the checkout spec                                          | same text, same source, one word                                                                                                                                                                                                                    |
| "read the terms" in `job exec`'s last line             | "read the spec"                                            | one word for the record everywhere it is named                                                                                                                                                                                                      |
| `magus session --brief`                                | unchanged                                                  | a summary flag with prior art (`diff --brief`, `file --brief`); not this feature's sense                                                                                                                                                 |

`JobResult` keeps its name: Eli did not pick status, and a result is filed once where a
status is overwritten. The row is not split into `spec` and `status` fields either; spec
names the parent's half of the row as it stands (see Alternatives).

Rejected names: terms (in-tree precedent in `job exec` and `render-brief.buzz`'s `Terms`,
but it names the agreement rather than the data, and its prior art is a lease, not a tool);
mandate (taken by [ADR 0019](0019-a-mandate-carries-out-decisions-already-made.md));
assignment, work order and charter (none has prior art in a build or VCS tool);
instructions (reads as prose to follow rather than conditions graded).

### 4. Kept, renamed, deleted

| piece                                                           | fate                                                                                   | State     |
| --------------------------------------------------------------- | -------------------------------------------------------------------------------------- | --------- |
| job row as the parent's record (ADR 0036 s1)                    | kept; gains `Decisions`, `Evidence`                                                    | planned (needs the boundary registry, `cmd/magus-utils/boundary_types.go`) |
| `JobResult` as the child's record (ADR 0036 s2)                 | kept unchanged                                                                         | done      |
| `Job.result` readable from a stop rule's `req.lease`            | kept                                                                                   | done      |
| `types/brief.go`, `magus\Brief` and its two helpers             | deleted; fields move to `types/job.go`                                                 | planned (needs the boundary registry) |
| `agents.brief_format`, `Job.BriefFormat`, `ResolveBriefFormat`, `BriefFormatUnset` | deleted                                                             | in progress (`Job.BriefFormat` waits on the regenerated bindings) |
| `judgeBriefBuzz` and the Buzz type check of a prompt            | deleted                                                                                | done      |
| render-brief.buzz                                               | replaced by `show-job.buzz` in the same directory, typed, two entry points             | done      |
| `magus describe job`                                            | kept as the terminal door                                                              | done      |
| `next` steps on structured results                              | kept; the view renders them as the worker's next commands                              | done      |
| a stop rule requiring a filed result (ADR 0036 s3, workspace)   | replaced by the shipped `stop-unfiled` below                                           | done      |
| `StopRequest.reply` (ADR 0036 s4)                               | not built: a reply judged for shape is prose parsed into data, the direction refused   | not built |
| `magus job view <id>` running a workspace-declared renderer     | not built: core names no repository script; the view is `magus buzz hack/dev/show-job.buzz` | not built |
| `brief-*` and `*-unbriefed` rule names                          | renamed as in section 3                                                                | done      |

### 5. The guard makes it explicit, at spawn and at stop

Every check is a deny with a reason and a command to run; none rewrites anything.

At spawn, shipped, deny by default:

- `spawn-unvalidated` (kept): the row the title names carries the `validated` stamp.
  Validation at `job fork` and `job apply` gains the requirements `render-brief.buzz`
  enforces at render time today: criteria, a check, write paths or `read_only`, and a
  model. The two refusals in `render-brief.buzz:271` move into validation, and the renderer
  stops judging.
- `spawn-command` and `spawn-plain-commands` (renamed): the prompt teaches no command the
  guard denies and, for an isolated worker, no compound command.
- `spawn-without-job-row` stays this repository's rule in `hack/policy/spawn.buzz`:
  whether a spawn with no row at all is refused is the workspace's call, as
  [ADR 0039](0039-judging-is-a-pure-computation.md) makes the spawn rule optional.

At stop, shipped, deny by default, named `stop-unfiled`:

- A `SubagentStop` for a caller whose `Lease` is set and whose `Lease.result` is null is
  refused once, naming `magus job exit <job> --stdin` and `magus job exit --schema`. A
  read-only row files a result too, with `changed_paths: []`, so a scout is held to the same
  rule. When `StopHookActive` is set the second stop passes, as the mandate's idle-stop rule
  already does, and `magus job wait` then shows the row never passed: the gap moves later,
  never out of sight.
- On Claude Code and Codex the deny becomes `decision: block`, so the child files the record
  itself. Cursor and OpenCode report no child stop, and the rule says nothing there.
- A workspace sets it to `advise` or `off` by name through `magus\guard.builtins`, as every
  shipped rule.

This reverses ADR 0036's "ship no such rule". The reason it gave, that a read-only scout may
not need to file, does not hold: `job exit` already ends a read-only row with nothing to
grade, so filing costs the scout one command and gives the parent one record to wait on.
Eli, 2026-11-10, asked whether it ships as written: "yes".

### 6. What it refuses to become

- A prompt template: prose with holes a parent fills. The row is the template, and it is
  data.
- A rewriting hook: `updatedInput`, `updatedToolOutput`, OpenCode's mutable `output.args`
  (ADR 0036, Alternatives).
- A reply parser: Markdown or JSON pulled out of `last_assistant_message` into a record.
  That is `StopRequest.reply`'s purpose, and it is not built.
- A second record beside the row, under any name.
- A format setting: `brief_format`, or a per-host record shape.
- A Markdown renderer in Go core that carries a repository's steps.
- A model call that summarizes a result, grades its quality or writes the spec.
- A message store or protocol between parent and child. The parent reads the result through
  `job wait`; the child reads the row through `describe job`.
- A re-prompt or retry of a child that filed nothing: `stop-unfiled` refuses once and
  `job wait` reports; nothing sends the child back a second time or forks for it.
- Stored Markdown: the render is read-time only, and a stale copy cannot exist.
- A required reply shape, length or vocabulary.

### 7. The migration is a deliberate break

magus is pre-1.0 and this is one change, one pull request, no shim and no alias kept for an old caller
([A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md)).

1. `types/job.go` gains `Decisions`, `Evidence`, `JobDecision`, `JobEvidence`,
   `JobEvidenceKind`; `types/brief.go` is deleted; `Job.BriefFormat` and
   `types.BriefFormat` go with it. `magus job exit --schema` and the job apply schema
   regenerate from the types.
2. The Buzz declarations regenerate through the ADR 0035 generator and are committed with
   their source. `magus\Brief`, `BriefDecision`, `BriefRef` and `BriefRefKind` leave the
   generated file because they left Go; nothing is hand-edited.
3. `agents.brief_format` leaves `internal/config/agents.go`. A `magus.yaml` still carrying
   it fails validation as an unknown key, naming the key and this record. No warning, no
   silent skip.
4. The job store reads a row carrying `brief_format` by dropping the key, pinned by a test
   that reads one such line; the row's next put writes it without. No reader branches on
   it, so this is not a compat shim and carries no marker.
5. `internal/guard/brief.go` loses `judgeBriefBuzz`, `briefInput.format` and
   `formatFrom`; the brief-format file is deleted (a7ce591b0); the rules are renamed and the
   rule pages regenerate from their descriptors. A workspace that set a renamed rule by its
   old name in `magus\guard.builtins` fails loudly with the new name.
6. `stop-unfiled` is added as a shipped stop rule with its descriptor and page.
7. render-brief.buzz becomes `show-job.buzz` in the same directory, reading typed
   records, with its fixtures renamed; `--extra` goes, since the prompt is where a parent's
   extra words live. `job exec`'s last line says "read the spec".
8. Guides, glossary (a `spec` entry), ADR 0036 (amended: s3 and s4 superseded here) and
   `changes/unreleased` name the break: the config key, the row field, the rule names and
   the deleted Buzz objects.

### 8. Comparing against the baseline

The scout running now records, from the trail's `agent_spawn` events and their prompt
blobs, the job store and the host transcripts: spawns; spawns whose title or first line
names a live row; results filed per leased spawn; refs filed per result
(`validation.output_ref` set or `owed` named, plus `verification_evidence` entries);
tokens per child, from the host's context window report; and reply bytes per child, from
the transcript's last assistant message. It writes the table with its window (sessions,
days, hosts) and date under `plans/`.

After this record is built, the same script runs over a window of the same size, and the
two tables sit side by side in the same file. Each measure has a direction it must move and
a reading that reopens the decision:

| measure                        | expected                                   | if not                                                                                   |
| ------------------------------ | ------------------------------------------ | ---------------------------------------------------------------------------------------- |
| results filed per leased spawn | rises toward 1.0                           | `stop-unfiled` is not reaching the child: check which hosts the stops came from          |
| `stop-unfiled` precision       | of denials, the share followed by a filed result, reported with its count | below 0.8, the deny costs turns and files nothing: set it to advise and say why |
| refs filed per result          | rises                                      | the view does not say which ref to file: fix the template, not the rule                  |
| prompt bytes per spawn         | falls, since the spec carries the record   | parents still paste the row: the view is being copied, measure what they add             |
| reply bytes per child          | falls, since the result carries the record | parents ask for prose: the result view is not reaching them                              |
| tokens per child               | does not rise                              | the spec's new fields cost more to read than the prose did: cut the template             |

A rate is reported with its numerator and denominator and its dates, never bare, as
[Measured, not asserted](../design/principles/measured-not-asserted.md) requires. The
comparison is one table a person reads; nothing in magus acts on it.

### Principles checked

- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  the prompt and the reply are the models' own text, unrewritten and unparsed; the records
  travel through the store, not through the hook.
- [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md):
  the stop rule ships as a deny, because ADR 0036 left it to prose and nothing was built;
  Eli confirmed the shipped rule on 2026-11-10.
- [One source, every door](../design/principles/one-source-every-door.md): `types.Job` and
  `types.JobResult` generate the JSON schema, the Buzz objects and the template's input;
  the CLI, a script and a hook read the same record.
- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  the record holds facts; the rendering is a Buzz function a person reads top to bottom.
- [No silent defaults](../design/judgment/no-silent-defaults.md): a removed key fails
  validation by name; a renamed rule fails by name; `stop-unfiled` says why it passed a
  second stop.
- [The host wiring is yours](../design/judgment/the-host-wiring-is-yours.md): the Markdown
  renderer and the repository's steps are repository Buzz; core adds no host-specific path.
- [Human in the loop](../design/principles/human-in-the-loop.md) and
  [The model proposes, the harness executes](../design/principles/the-model-proposes-the-harness-executes.md):
  magus refuses a stop and reports a comparison; a person reads both and decides.
- [Delete before you add](../design/principles/delete-before-you-add.md): five things are
  deleted (the Brief types, the format key, the row field, the Buzz type check,
  `StopRequest.reply`) for two fields and one rule added.
- [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  the break is listed in section 7 and in `changes/unreleased`, and any gate it trips gets
  its deviation line in the same diff.
- [Flags, config and help from one source](../design/principles/flags-config-help-one-source.md):
  the config key leaves the one source, and its help, completion and schema leave with it.

## Alternatives

### Keep `brief_format: buzz`, a typed literal in the prompt

A `magus\Brief{...}` literal the guard type-checks. It lost because the literal duplicates
the row in a place the store never sees, so the two drift; the guard checks text no person
reviews in version control; and Codex's v2 prompt is encrypted, so the check cannot run
there (ADR 0036, spawn table). Evidence: it ran nowhere, this repository included, in the
two days since it merged. Reopen if a host offers a typed spawn input field that reaches
the child as data rather than as prompt text.

### Split the row into `spec` and `status` fields

Nest the parent's half under `Job.spec` and the store's and child's half under `Job.status`,
as a Kubernetes resource does. The word spec wins (section 3); the nesting lost, because it
touches every reader of `types.Job` and adds no capability this decision needs: the row
already separates declared fields from store-computed ones in its comments, and
`Generation` already marks a spec change. Reopen when a reader needs the declared half
alone, such as a diff of a row between generations.

### Call it the terms

The in-tree word: `job exec` says "read the terms" and `render-brief.buzz` declares `Terms`.
It lost to spec on Eli's call, 2026-11-10, and on what the word names: terms are the
conditions of a lease, an agreement, where spec names data a store validates and a view is
rendered from. Reopen never; the word stays available for the lease it describes.

### Markdown as the source, with a parser

Let the parent write prose and have magus pull a record out of it. It lost on direction:
Eli's words, and the fact that no parser of prose is total, so every miss is a silent
default. Reopen never on these grounds.

### Leave the stop rule to the workspace (ADR 0036 s3)

It lost because a rule nobody is required to write is a rule nobody writes: two days after
ADR 0036 merged, no workspace rule existed, this repository's included, and the scout's
baseline is the first count of unfiled results. Reopen if that baseline already shows 0.9 or
more results filed per leased spawn, which would make the deny noise.

### `magus describe job -o markdown` in Go

One flag, every workspace. It lost because the view a worker reads carries this repository's
steps (the bootstrap script, the skill to load, which target it may run), which core never
names ([The host wiring is yours](../design/judgment/the-host-wiring-is-yours.md)), and a
generic Markdown of the row is `-o template=` already. Reopen if the view shrinks to
nothing repository-specific.

### A `magus job view <id>` verb in core

One verb on every host, running the repository's `show-job.buzz`. It lost because core
never names a repository's Buzz script path, and a verb that ran a hard-coded `hack/` path
would be the first. The declared route (a renderer registered in the magusfile, a verb that
refuses by name when none is) stays open and is not built: `magus buzz hack/dev/show-job.buzz`
is already one command. Reopen when a second workspace renders a view and wants the same
door, or a host needs the view without a shell.

### Keep the word brief

It lost on Eli's call, and on the three senses the word carries in the tree (a prompt, a
skill load, a summary), which the renames separate. Reopen never.

## Consequences

- A parent spawns with a title that names the row and a prompt of its own words. Everything
  the child must honor is on the spec, readable with `describe job` and renderable as a view.
- A leased child on Claude Code or Codex cannot stop without filing a `JobResult`; on Cursor
  and OpenCode the gap shows in `job wait`.
- The Buzz declarations lose four objects and gain two fields and one enum, all generated.
- A `magus.yaml` carrying `agents.brief_format`, a row carrying `brief_format`, a workspace
  naming a `brief-*` or `*-unbriefed` rule, and a script calling `render-brief.buzz` each
  fail by name once this merges. There is no other migration.
- The Markdown a person reads is rendered from the record at read time, so it cannot
  disagree with the row.
- The comparison in section 8 is the first measurement this feature has had.

## Open questions

- Whether `JobEvidenceKind` needs a `session` kind for a transcript, which a parent today
  cites by path in prose.
- Whether the Codex `SubagentStart` context should carry the rendered view rather than
  `describe job`'s terminal text, which would make the Buzz renderer the one render on that
  host too.
- The number: main ends at 0039 at this writing, and 0040 to 0042 are on open branches.
  Recheck at merge.
- What the scout's baseline window is, so the after-window can match it.

## Amendments

### 2026-11-10: steps 3, 5, 6 and 7 built; steps 1, 2 and 4 wait on the bindings

`judgeBriefBuzz`, `ResolveBriefFormat`, the doctor's brief-format check and
`agents.brief_format` are gone, the rules carry their new names, `stop-unfiled` ships,
and `hack/dev/show-job.buzz` renders a spec and a result from `magus\Job` and
`magus\JobResult`. `Job.BriefFormat`, `types/brief.go` and the two new row fields wait on
the boundary registry and the regenerated bindings, which another branch holds. Two
pieces of section 7 stay open with them: a `magus.yaml` still naming the removed key
fails as an unknown key without naming this record, and validation at `job fork` does not
yet require criteria and a model, since most of the scripted tests fork without either.

### 2026-11-12: the brief-format file and render-brief.buzz are gone

The brief-format file that Context and section 7 step 5 name was deleted in a7ce591b0
("feat(guard)!: drop the brief format", #730). Nothing replaces it: section 6 refuses a
format setting, so there is no code to point at. render-brief.buzz was deleted in the same
commit, and its replacement was added as hack/dev/job-view.buzz, since renamed to
`hack/dev/show-job.buzz`, which section 2 names. Context is left as read on 2026-11-10,
so its Markdown mentions of the old paths are history, not current references.
