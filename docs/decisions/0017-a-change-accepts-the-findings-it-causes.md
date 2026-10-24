---
title: "ADR 0017: a change accepts the findings it causes"
order: 17
description: A deliberate protobuf rename fails buf-breaking and has no sanctioned way through the gate. This record proposes a per-project ledger of accepted findings, read by the target runner, in which an entry counts only while the change that adds it is not yet in the base. It names one target, one op and each finding it accepts, carries a reason, passes the gate green with the acceptance printed, and needs no cleanup after merge.
tags: [adr, decision, ci, gates, compatibility, buf, breaking-changes]
status: proposed (reopened 2026-10-24 to reuse what exists)
date: 2026-10-24
---

# ADR 0017: a change accepts the findings it causes

## Context

The rename of spells to cantrips renames about ten protobuf fields (`spell` to `cantrip`,
`spell_name` to `cantrip_name`, `spell_resolve_count` to `cantrip_resolve_count` and
others, across the graph, insight, metrics, status, tool and viewer packages), counted
from the uncommitted diff on `feat/spells-to-cantrips-f67682` on 2026-10-24. Under
`breaking: use: [WIRE_JSON]` (`proto/buf.yaml`) each one is a finding, and `proto`'s
`ci` target runs `buf-breaking` against `vcs\base()`. The rename is intended. Today the
change has four ways forward, and each is wrong:

- merge red, which is never acceptable;
- add `breaking.ignore_only` to `proto/buf.yaml`, which waives a rule for whole files and
  stays until someone remembers to remove it;
- set `ignore_unstable_packages: true`, which turns the gate off for all eleven packages,
  since every package is `v1alpha1`;
- remove `buf-breaking` from `ci`.

The gate is worth keeping even before 1.0. The console is a wire client whose service
worker can serve a bundle from months ago, and the wire API's support window is the
current release and the one before it
([Compatibility](../concepts/compatibility.md#client-and-daemon)). A JSON field rename
breaks that bundle. What the gate owes the person is that such a break is never
accidental. It does not owe them that a break never happens:
[Break now, hold still later](../design/principles/break-now-hold-still-later.md).

Four facts shape the answer.

1. **A comparison gate's finding lives exactly as long as the change is unmerged.**
   `buf-breaking` judges the tree against a base. Once the rename is in the base, the
   next change's run no longer reports it. A lint finding is different: it is a property
   of the tree and persists after merge, and the tool's own suppression comment
   (`//nolint:rule`, `buf:lint:ignore`) already answers it.
2. **Charms are run-wide.** A charm reaches every target in a run through `ctx.needs`,
   and `ctx.hasCharm` reads the run's set. `Magus.RunCI` strips only `rw` and `update`
   (`CharmsForCI`, `run.go`), so a workspace charm such as `breaking` would reach every
   target in `magus affected ci`. It cannot name one finding.
3. **CI runs the gate in three places, and the decision must reach all three.** A
   pull request runs against its merge base, a merge-queue candidate against main's tip,
   and a push to main against the commit main last passed at (`last-passed`,
   `.github/workflows/ci.yaml`). A decision held in pull-request metadata, such as a
   label, never reaches the push run. A decision held in a commit message reaches it
   only if the squash keeps the message.
4. **The person works uncommitted.** The rename above is uncommitted as this is
   written. A local `magus run ci proto` before a commit must reach the same verdict CI
   reaches after it.

magus already reads tool findings as records: a spell declares
`Tool.diagnostics = DiagnosticFormat.gnu` and magus parses file, line, severity and
message ([Declaring how a tool reports findings](../guides/authoring-spells.md#declaring-how-a-tool-reports-findings)).
The GNU shape carries no rule id, so no finding has an identity to accept. buf 1.58.0,
the version on this machine, offers `--error-format` values `text`, `json`, `msvs`,
`junit`, `github-actions` and `gitlab-code-quality`. The last is the Code Climate JSON
record, which carries a rule (`check_name`), a path and a description, and is also
emitted by golangci-lint, ESLint and RuboCop formatters.

Measured on 2026-10-24 by running `buf-breaking` through a magus target on the cantrip
tree: buf exits 100 on findings, as distinct from 1 on a tool failure, and its Code
Climate `fingerprint` hashes the position. A one-line edit above `tool.proto`'s fields
changed every fingerprint and no description, so the description, not the fingerprint, is
the stable part of a finding. Each field rename yields two findings,
`FIELD_SAME_NAME` and `FIELD_SAME_JSON_NAME`.

### Security scans want the same thing, with a different lifetime

`hack/magusfile/advisories.buzz` judges `pnpm audit` and `govulncheck` itself. A
finding with no installable patch, or one younger than the ten-day `minimumReleaseAge`, is
deferred automatically: printed, never fatal, and blocking on its own the day the patch
matures. A finding the base already carried is let through on a change and fails main.
No person decides either case, and the file says why: "no allowlist to rot". What it
lacks is the case this record is about: a blocking finding a person decides not to take
yet, with a reason.

A security finding outlives the merge, unlike a comparison finding. A line that stops
counting at merge would let the change through and fail main's next scheduled run. So a
scan's acceptance has to last while the finding is still reported, and fail as unused
once it is not.

### What already exists

This record's first draft added a finding record and a waiver record beside ones the tree
already has. The inventory, from `magus query "kind=symbol ..."` and `magus refs` on
2026-10-24:

| record                                        | fields                                                                                                        | who uses it                                                            |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `types.Finding` (`magus/lint` in Buzz)        | Path, Line, Col, Rule, Message, Fix, URL                                                                      | 38 Buzz files: every `hack/lint` rule, docs conventions                |
| `spells.Diagnostic`                           | File, Line, Col, Severity, Code, Message                                                                      | nothing outside its tests; `ParseDiagnostics` has no production caller |
| `Finding` in `hack/magusfile/advisories.buzz` | id, installed, summary, fix, actionable, deferral                                                             | the two security gates                                                 |
| `types.JobProblem`                            | Code, Field, Subject, Message, Fix, Edit                                                                      | job record validation                                                  |
| `types.JobWaiver`                             | Code, Subject, Reason; `Waives` matches code and subject, a glob for two codes; `Waivable` limits which codes | job records (MGS3043, MGS3044, gate not run)                           |
| proofread `Finding` and suppressions          | rule, span, reason required; unused is PRF1090                                                                | `libs/proofread`, a library that never imports magus                   |
| target `advisory` and `advisory_reason`       | a whole target excused from a composite, with prose                                                           | magusfiles                                                             |
| the conformance ratchet                       | a committed record of known violations, pruned by `format`                                                    | `hack/lint/code-stays-within-the-conformance-ratchet.buzz`             |

The first four are one idea under four names: something a check says about a place, with
a rule and a message. `types.JobWaiver` is the waiver this record needs, already with a
required reason, a code-and-subject match, and a list of what may be waived. Claims
([ADR 0010](0010-jobs-share-a-file-by-declaration-claims.md)) decide who may write a file
and are not a waiver. proofread keeps its copy: it is a library with no magus dependency
([A copy can be the right answer](../design/principles/a-copy-can-be-the-right-answer.md)).

### Names already taken

| word       | already means in this tree                                                        |
| ---------- | --------------------------------------------------------------------------------- |
| diagnostic | magus's own coded errors: `DiagnosticCode`, `DiagnosticError`, the MGS pages      |
| finding    | a lint rule's report, a proofread report, a conformance item, a security advisory |
| problem    | a job record that fails validation                                                |
| advisory   | a target excused from a composite, a guard rule that advises, a security advisory |
| verdict    | a gate's result, in 244 docs pages                                                |
| feedback   | the guard's feedback marks in the activity trail (`internal/trail`)               |
| waiver     | a job record's accepted problem                                                   |

## Decision

Reopened on 2026-10-24, after Eli accepted the first draft and before anything was
built: the draft below adds records the tree already has. The direction it keeps is the
ledger, the two-way check, and liveness read from the base. What changes is that it is
built from the existing finding and waiver records, under names Eli picks. The questions
are under Open questions; the draft stays below so the redesign starts from it.

### Reuse before adding (proposed)

1. **One record for what a check says about a place.** `types.Finding` is the one 38
   scripts already return. It gains `Severity`. `spells.Diagnostic` is deleted, and
   `ParseDiagnostics` returns `[]types.Finding` from beside its first caller, since
   `types` imports `spells` and not the reverse. `advisories.buzz` returns `lint.Finding`.
   `types.JobProblem` stays separate for now, because its place is a JSON field of a
   record, not a file.
2. **One waiver record.** `types.JobWaiver` becomes the shared waiver, keeping
   `Waives`. Jobs carry it in the record, and the ledger carries it per line, scoped to a
   target and an op. A finding's subject for matching is its path and message, without
   the line. Matching stays one method, so a waiver means the same thing on a job and a
   gate.
3. **The lifetime belongs to what produced the finding, not to the waiver.** A
   comparison op (`External: compares-base`) counts only the lines this change adds. A
   scan counts every line while the finding is reported, and fails an unused line only
   when this change added it.
4. **Policy is not a waiver.** The scans' automatic deferrals stay in the gate's own
   code, and report through the same result as a waived finding, naming their condition
   instead of a ledger line.

### The ledger is a file the change adds lines to

`<project>/accepted.jsonl`, one JSON object per line, one line per decision:

```json
{"target":"ci","op":"buf::buf-breaking","reason":"spells are cantrips; an older console bundle reloads on the version mismatch","findings":[{"rule":"FIELD_SAME_JSON_NAME","path":"magus/tool/v1alpha1/tool.proto","message":"<the description buf prints for this finding>"}]}
```

The message is a placeholder here: the real one is whatever buf prints, copied from the
line the failing gate offers.

- `target` and `op` name exactly one op of one target. An entry never reaches another
  target, or another op of the same target.
- Each finding is matched on rule, path and message. Line and column are left out, so an
  edit above the field does not unmatch it.
- `reason` is required and not empty. A line without one is a load error.
- People write the lines, never magus. A failing gate prints each unaccepted finding
  as the line to add, with an empty `reason` that the person fills in. Accepting a break
  is a rep ([Manual on purpose](../design/judgment/manual-on-purpose.md)), so no verb or
  `:rw` writes the ledger.

### An entry counts only while its change is unmerged

An entry is **live** when its line is added in the diff from the run's base to the
working tree, the same base and the same diff `magus affected` reads. Every other line
is history. It is never pruned and matches nothing.

That one predicate does the work an expiry date would, without a date:

- after merge, the base holds both the rename and the line, so the finding is gone and
  the line is no longer live. The next change answers only for what it did;
- the push run on main diffs from `last-passed`, so the line is still live there and main
  stays green;
- a revert renames the field back. That is a break of the new base and needs its own
  line, which is honest;
- a reworded line is a removed line plus an added one, so it is live again and must
  match a finding.

The ledger's history is the record of every break shipped on purpose and why, which
`git log -p accepted.jsonl` reads.

### The gate checks both directions

| check                                              | result                                                                        | State    |
| -------------------------------------------------- | ----------------------------------------------------------------------------- | -------- |
| a finding no live entry accepts                    | the gate fails as today, printing the line to add                             | proposed |
| a live entry that matches no finding               | the gate fails with a new code: the acceptance is unused                      | proposed |
| every finding accepted, at least one               | the target passes as `accepted`, never as `pass`                              | proposed |
| an op that exits non-zero and prints no findings   | the failure stands; a crash is never accepted                                 | proposed |
| a malformed line, an empty reason, an unknown op   | a load error ([No silent defaults](../design/judgment/no-silent-defaults.md)) | proposed |
| a ledger entry for an op not declared a comparison | a load error                                                                  | proposed |
| no base resolves (shallow clone, no VCS)           | an error naming the base, never "all live" or "none live"                     | proposed |

The unused check is what keeps a line honest: it cannot be added "in case", and it fails
the moment the change it accepted is reworked away. It charges only the change that adds
the line, because only that change's lines are live.

### Only a comparison op can be accepted

The declaration folds into the one that already exists. `spells.External` names an op
whose answer "can differ with the tree byte-identical" (`spells/descriptor.go`), today as
`reads-external` and `mutates-external`. A comparison against the base is the third
such relation, `compares-base`: the tree holds still and the base moves. `magus doctor`
already reads `External` for cacheability (MGS1033), so a comparison op needs no second
cacheability rule. magus hands an op that declares it the run's base. `bufBreaking`
declares it and builds its `--against` input from that base, so the base the ledger is
diffed against and the base buf compares against cannot disagree. `proto/magusfile.buzz` stops building `.git#ref={base}` itself. A
ledger line naming an op that compares nothing is refused, because a finding about the
tree outlives the merge and its line would charge the next change.

The op names a findings format with a rule id. The first is the Code Climate record
(`DiagnosticFormat.codeClimate`), which magus parses once for every tool that emits it;
`bufBreaking` passes `--error-format=gitlab-code-quality`. It records into
`types.Finding` (see Reuse before adding), and `ParseDiagnostics`, which has no caller
outside its tests today, gets its first.

`bufBreaking`'s `gha` charm swaps buf's reporter to GitHub annotations, which would leave
magus no findings to match. It goes: magus annotates from the parsed findings through
the CI provider spell, accepted or not.

### Every reader sees it

- The run line reads `proto ci: accepted <n> findings (proto/accepted.jsonl:<line>)`.
- `-o json` carries each accepted finding and the ledger line that matched it.
- `magus affected ci` lists every accepted target in its summary, beside the failures.
  An accepted gate is a deviation, so it prints at the default verbosity
  ([No news is good news](../design/principles/no-news-is-good-news.md) spends output on
  what changed).
- A CI provider spell's `annotate` op receives each acceptance as a notice on the ledger
  line, so a pull request shows it beside the diff.
- An accepted target is never cached. Its verdict depends on the base, and a comparison
  op is uncacheable already (`skip_cache` on `proto`'s `ci`).

### Where each rule lives

Per [Where a rule lives](https://github.com/egladman/magus/blob/main/CONTRIBUTING.md#where-a-rule-lives):

| rule                                        | home                                                                          | State    |
| ------------------------------------------- | ----------------------------------------------------------------------------- | -------- |
| an op compares against a base; its format   | a spell declaration, checked at load                                          | proposed |
| a finding is unaccepted, an entry is unused | a coded error from the target runner, naming the line to add or delete        | proposed |
| the ledger's shape and its required reason  | a load error (misconfiguration)                                               | proposed |
| who may write the ledger                    | nothing new: a leased worker writes it only if its write paths name it        | proposed |
| who approves a line                         | the pull request review, the existing point where a person decides what lands | proposed |
| why it is built this way                    | this record; refusal rows for the charm, the trailer and the label            | proposed |

### Build order

One pull request, on this record's branch, one commit per step.

| step                                                                                                                           | State    |
| ------------------------------------------------------------------------------------------------------------------------------ | -------- |
| 0. capture buf's Code Climate output and exit status for the cantrip rename through a magus target; keep it as the fixture     | proposed |
| 1. `spells.External` gains `compares-base`; a declaring op receives the run's base; MGS1033 counts it uncacheable              | proposed |
| 2. `DiagnosticFormat` gains the Code Climate record, parsed by `ParseDiagnostics` into rule, path, line and message            | proposed |
| 3. the ledger entry type in `types/`, its JSON on the type, the per-project loader and its load errors                         | proposed |
| 4. liveness: the ledger's added lines from the run's base, through the VCS driver, on every backend; no base is an error       | proposed |
| 5. the target runner matches findings, fails unaccepted and unused, and reports `accepted`                                     | proposed |
| 6. `accepted` reaches the run line, `-o json`, the `affected ci` summary, the CI provider's `annotate` op and the console      | proposed |
| 7. `bufBreaking` declares `compares-base` and the format, drops `gha`; `proto/magusfile.buzz` drops its hand-built `--against` | proposed |
| 8. docs, diagnostic code pages, refusal rows and a changelog fragment                                                          | proposed |

The cantrip pull request lands after this one and adds `proto/accepted.jsonl`.

### Doctrine checked

- [Verdicts are provenance-blind](../design/principles/verdicts-are-provenance-blind.md):
  relied on. The gate reads the ledger's bytes and the base, never who wrote the line.
- [Human in the loop](../design/principles/human-in-the-loop.md) and
  [Agents propose, humans dispose](../design/judgment/agents-propose-humans-dispose.md):
  relied on. An agent may propose a line; it lands only through the review that lands
  the rename. No new point is added.
- [Manual on purpose](../design/judgment/manual-on-purpose.md): relied on. magus prints
  the line and never writes it.
- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. `accepted`
  is its own result, an unused line fails, and a missing base is an error.
- [Friction that carries meaning](../design/principles/friction-that-carries-meaning.md):
  relied on. The friction is a reason per decision and a line per finding, which is the
  information a reviewer reads. A finding charged to a change that did not cause it is a
  toll, and liveness prevents it.
- [Told, never guessed](../design/judgment/told-never-guessed.md): relied on. The
  verdict is the same on a laptop and a runner because it reads only the tree and the
  base.
- [Every answer has an address](../design/principles/every-answer-has-an-address.md):
  relied on. No date and no host state enter the verdict.
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md):
  the failure removed is a blocked pull request whose break is intended, with every
  current path forward either red, permanent or gate-disabling.
- [Delete before you add](../design/principles/delete-before-you-add.md): weighed. The
  deletions available (drop the gate, mark packages unstable) remove the protection the
  console relies on; see Alternatives.

## Alternatives

Each option names two cases its predicate cannot tell apart, and what a wrong firing
costs.

### A workspace charm such as `breaking`

Lost. Its predicate is "the run carries the charm". It cannot separate the intended
rename from an accidental field deletion in the same change, and it cannot separate
`proto`'s `ci` from any other target whose code reads `ctx.hasCharm("breaking")`. A
wrong firing ships an unreviewed break. CI also has no way to learn the charm: it lives
on the command line, not in the tree, so the push run on main lacks it and main goes red
after a green pull request. Reopen if charms gain a per-target scope that a committed
file selects; that is this ledger at target granularity, less precise.

### A charm scoped to one target (`ci:breaking` on `proto` only)

Lost. Narrower in target, still blind to the finding: the rename and an accidental
deletion in the same target both pass. `magus affected ci` names no per-target token, so
the scope would have to live in a committed file, and then that file is the ledger with
less in it.

### A commit trailer (`Accepts: proto ci FIELD_SAME_JSON_NAME ...`) over base..HEAD

Lost, narrowly. It is self-expiring and can name a finding. Its predicate cannot
separate an uncommitted tree from the same tree committed, so the local verdict before
the commit differs from CI's after it, and the rename in Context is uncommitted today.
It cannot separate a squash that keeps the commit list from one that keeps only the
pull request title and body, so the push run on main can lose the trailer and go red
after a green pull request. A trailer is also not in the diff a reviewer reads, and the
bad example on
[Verdicts are provenance-blind](../design/principles/verdicts-are-provenance-blind.md)
is a gate keyed on a trailer. Reopen if every supported host and squash mode is shown to
keep trailers and local gating is always at a commit.

### A pull-request label, a ruleset bypass or an admin merge

Lost. The predicate is host state. It cannot separate the pull-request run from the push
run (the label is absent on push), or a laptop from a runner (a laptop has no label). A
wrong firing is a red main, or a merge with a red required check, which is never
acceptable. It also puts a provider read into the gate's verdict
([Provider I/O is Buzz](../design/judgment/provider-io-is-buzz.md)).

### `breaking.ignore_only` in `proto/buf.yaml`, added and later removed

Lost. Its predicate is rule plus file. It cannot separate the rename from any other break
of the same rule in the same file, and it cannot separate the change that needed it from
every later change, because it persists until someone deletes it. That deletion is a
chore for a bystander or never happens. A wrong firing ships a later break unseen. buf's
own documentation does not recommend `ignore_only` or `except`.

### `ignore_unstable_packages: true`

Lost. buf's convention says `v1alpha1` packages may break, and every package here is
`v1alpha1`, so this is one line and no magus code. Its predicate is the package suffix:
it cannot separate the intended rename from an accidental deletion, in any of the eleven
packages, for as long as they stay alpha. A wrong firing breaks the cached console
bundle the wire contract exists for. Reopen if the console stops reading these packages
over the wire, or if Eli decides the wire window does not hold before 1.0; then the gate
should be deleted, not neutered.

### A committed baseline image that a dedicated target rewrites

Lost, as Eli judged it. `buf breaking --against proto/baseline.binpb`, with a target
under `:rw` that rewrites the image. Its predicate is "the image was rewritten". It
cannot separate the rename from every other break present in the tree when the rewrite
ran, because a rewrite accepts all of them at once. It cannot separate what consumers
run from what the image says, because the gate stops comparing against the base. The
diff is a binary a reviewer cannot read, and two pull requests that rewrite it conflict.
The ledger differs on each count: it is append-only text written by a person, it
accepts named findings, and the comparison stays against the real base.

### A ledger whose entries are always live

Lost. Its predicate is "an entry matches a finding". It cannot separate this rename from
a later rename of the same field back and forth: a revert followed by a re-break reports
the same rule, path and message, and the old line accepts it unseen. It also cannot
detect an unused line, since every historical line is unused. A wrong firing ships a
break with a stale reason.

### Prune the ledger after merge

Lost. Deleting lines once they stop being live (by `format:rw`, or when a release is
cut) charges a bystander: the next change to touch the project carries a deletion it did
not cause, which [Rolling out a stricter rule](https://github.com/egladman/magus/blob/main/CONTRIBUTING.md#rolling-out-a-stricter-rule)
rules out. Liveness makes pruning unnecessary, and the history is the audit trail.

### An expiry date on each entry

Lost. Its predicate is the clock. It cannot separate a change merged before the date
from one still in review after it, so the same commit gets two verdicts on two days, and
an approved shim's condition in this repository is something observable, never a date.
Base containment is the observable condition.

### Move `buf-breaking` to a `human_only` target, or out of `ci`

Lost. Its predicate is "nobody ran it". It cannot separate any break from any other. A
wrong firing is every accidental break.

### A new package version (`magus.tool.v2`), buf's route for a deliberate break

Lost before 1.0. It duplicates every service for a field rename, which
[Break now, hold still later](../design/principles/break-now-hold-still-later.md)
exists to avoid. It is the right route after 1.0, and the ledger does not replace it
there: an accepted line still ships a break.

### A guard rule that denies an agent writing the ledger

Lost. The guard grades a write by who acts only for lease ownership, and a leased
worker already cannot write a path its lease does not name. A rule keyed on an agent
writing a ledger line would move the bar with the byline. The person decides at review.

## Consequences

- The cantrip pull request adds `proto/accepted.jsonl` with one line, its findings and a
  reason, and passes `ci` green with the acceptance printed. Its fragment under
  `changes/unreleased/` still carries the `Breaking:` headline; the ledger is the proof,
  the fragment the release story, as the
  [acceptance model](../guides/migrating/breaking-changes.md#acceptance-model) already
  divides them.
- Every comparison spell (buf today; an API diff or a migration check later) gets
  acceptance by declaring itself a comparison and naming a format, with no per-tool code
  in magus.
- Lint-style findings stay with each tool's own suppression comment. The ledger refuses
  them.
- A buf upgrade that rewords a message unmatches in-flight lines. The gate fails loud in
  both directions and the person re-pastes. That is the safe direction.
- `proto/magusfile.buzz` loses its hand-built `--against` string.
- A new run result, `accepted`, reaches the run record, `-o json`, the console and the
  CI summary. Each reader must handle it; one that does not is a bug, not a pass.

## Open questions

- **The record's name.** "Finding" is the most used today, but it also names proofread's,
  conformance's and the advisories' records; "diagnostic" is taken by magus's own MGS
  errors; "feedback" by the guard's trail marks; "problem" by jobs. Prior art says
  "result" (SARIF), "issue" (Code Climate) and "violation" (Checkstyle). Each is a
  rename across 38 Buzz files and the `magus/lint` module.
- **The waiver's name, and the result's.** `waiver` already exists in the job schema
  and the MGS3043 and MGS3044 pages; `accepted` was this draft's word, which would make
  two words for one act. The run result could read `waived`.
- **Whether `JobProblem` folds in too.** Its place is a record field, not a file path;
  folding it would make `Path` mean two things. Kept apart in the proposal.
- **Whether `JobWaiver`'s globs carry over.** Two job codes accept a glob subject. A gate
  waiver with a glob would accept findings nobody read, so the proposal keeps gate
  subjects exact, which makes `Waives` branch on the waiver's use.
- **The ledger's file name**, and the name of the comparison declaration
  (`compares-base`).
- **One line per decision or per finding.** One waiver per line, as `JobWaiver` is shaped,
  repeats the reason for each of the 20 findings the cantrip rename makes.
- Whether the number holds at merge: check the base branch for the next free ADR number.

## Amendments

### 2026-10-24: accepted, with the declaration folded into `External`

Eli accepted the ledger design. The comparison declaration became a third
`spells.External` value instead of a new field, since `External` already names an op
whose answer moves while the tree holds still. That settles the open question of who
owns the base: magus does, for every op declaring `compares-base`. The `gha` charm on
`bufBreaking` is dropped, and a build order was added under Decision.

### 2026-10-24: reopened to reuse the finding and waiver records

Before any code landed, Eli asked that the security scans in
`hack/magusfile/advisories.buzz` use the same mechanism, and then stopped the build: the
draft's `AcceptedFinding` and `AcceptedEntry` repeated `types.Finding` and
`types.JobWaiver`. The draft code was deleted unmerged. Context gained the scans'
lifetime, the measured buf exit status and fingerprint, the inventory of existing
records and the names already taken; Decision gained Reuse before adding; the status
went back to proposed and every State row to proposed until Eli redesigns it.
