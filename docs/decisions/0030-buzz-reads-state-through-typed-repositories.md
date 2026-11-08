---
title: "ADR 0030: buzz reaches state through typed repositories"
order: 30
description: Buzz in this repository stops naming JSON Lines rows, store paths, line splitting and last-line-wins merges. Each store is a Go repository that owns its location and keeps an unexported persistence type; Buzz reaches it through doors declared once and typed by boundary types. Row stays below the boundary.
tags: [adr, decision, state, buzz, host-modules]
status: proposed (Eli reads it before any code)
date: 2026-11-07
---

# ADR 0030: buzz reaches state through typed repositories

## Context

[Everything is a file](../design/principles/everything-is-a-file.md) makes each store
one append-only `.jsonl` with one owner, and says a reader hands each line to the entry
type and never splits lines itself. Measured on `feat/practical-moore-0e0a38` at
d29cea148, 2026-11-07, with `magus query`, `magus refs`, `magus describe module magus
-o json`, a grep fallback, a `magus buzz` spike and a go-review-ultra pass (style, nit,
skeptic) over the store layer:

### Buzz reaches into encodings

14 call sites in 10 hand-written Buzz files:

| file | store | what it does | door today |
| --- | --- | --- | --- |
| `hack/policy/mandate.buzz` (4 sites) | mandate, mandate attention | names `mandate.jsonl` and `mandate-attention.jsonl`, splits, last line wins, appends | none: repo Buzz owns the store |
| `hack/dev/issue-mandate.buzz` (3) | mandate, attention | appends, splits, last line wins | none |
| `std/lib/diskwatch.buzz` | disk samples (`$XDG_STATE_HOME/magus/disk/samples.jsonl`) | the only writer: `append`, compaction, `read`, `storePath` over `fs`; Go's doctor reads the same file | none: two owners |
| `hack/retro/trace.buzz` | guard trail | reads `events.jsonl` and blobs, splits, filters | `session().trail()`, which drops the request ref |
| `hack/retro/cursor-audit.buzz` | guard trail | reads `.magus/activity/events.jsonl`, splits | `session().trail()` |
| `hack/ci/show-failures.buzz` | `.magus/logs/failed-outputs.jsonl` | names the path, splits; a shell step in `.github/workflows/ci.yaml:836` writes it | none: no magus owner |
| `hack/dev/bootstrap-checkout.buzz` | jobs | parses `magus ls jobs -o json` text | `workspace().jobs()` |
| `hack/dev/bootstrap-worktree.buzz` | jobs | parses `magus describe job -o json` text | `magus\job(id)` |
| `hack/dev/count-feedback.buzz`, `merge-job-branches.buzz` | marks, jobs | decode fixture lines in tests | `session().marks()`, `workspace().jobs()` |

### The Go layer under the doors is uneven

- The persistence primitive is `file.JSONL[T]` (`internal/file/jsonl.go`: `Load`,
  `Update(fn)` under a lock, `Key`, a `Live` tombstone, compaction by rename). It folds
  entries in order of each key's last line. About ten stores use it.
- `job.Store` is a real repository: path, line format, two locks and compaction are
  private, and nothing outside `internal/job` opens `jobs.jsonl`. It stays off
  `file.JSONL` because the file has a header line (schema, migration, retention),
  migrations run under the lock, writes append only changed entries, delete is a
  rewrite, and `List` keeps first-appearance order. It also exports `Path()`, which
  `internal/guard/grant.go` uses to place `gate-waivers.jsonl`. Its side ledgers
  (`internal/job/landings.go`: landing probes, unmerged facts) hand-roll the same read,
  diff and rewrite with no file lock and drop their write errors.
- The trail and feedback marks are not repositories. `trail.Append`, `ReadRecent`,
  `LastRun`, `Stat`, `Rotate`, `WriteBlob`, `ReadBlob` are free functions taking a
  `base` directory that about 30 callers compute. `Rotate` (the compaction) is a public
  verb with no lock, so an append during it is lost. Four readers re-split
  `events.jsonl` with `bufio.Scanner` and skip corrupt lines silently; `LastRun` turns
  an unreadable trail into "never ran". Marks are two free functions and a `dir`; the
  reader has no partial-line handling and the writer no partial-tail repair, so one
  crashed append makes every later `session().marks()` fail.
- `review.Store` does not exist; `review` uses a bare `file.JSONL[Receipt]`.

### The doors are hand-written five times and untyped where they write

- Each door is written in `std/magus_objects.go` (declaration), `std/magus_handle.go`
  (a forwarder over a second exported function: `MagusWorkspaceJobs` over
  `MagusListJob`, `MagusSessionMarks` over `MagusTrailMarks`), `internal/dry/host.go`
  (dry-run mirror, where `Job.wait` returns null), `internal/guard/mcp.go` (the
  read/write classifier, a switch with no default and no case for `Session.mark`) and
  `internal/guard/cost.go:1110` (the cost list). Only the Buzz declarations and
  `internal/interp/bindings/gen` are generated.
- `putJob(opts map[string]any)`, `Job.exit(map)`, `Job.wait(map)` and
  `Session.mark(map)` decode by hand at run time; `types.JobPut` is not a boundary
  type, and `Session.mark` has a 48-line decoder although `TrailMark` is one.
- `MagusTrailRead` derives the cache dir, the checkouts and the window in `std`, not in
  `trail`, and falls back to one checkout on any error without saying so.

### Row is in the boundary

`magus\ConformanceRow` and `ConformanceReport.rows` (`types/conformance.go`, proto
`historymetrics.v1alpha1`), `ToolRow`, `ModelRow` (the same record is `ModelPricing` in
the job store), the `{definition, rows}` fields on ModelTiers and Grouping, about 20
"row" in the Buzz docs in `std/magus_objects.go`, and messages such as
`magus\job: no row has id` and `is not a row id`.

### Out of scope

Foreign JSON Lines magus reads but does not own (agent transcripts in `hack/retro`, the
session loaders under `docs/guides/integrations/agents/`, `libs/embed/train`), the
committed `*.deviations.jsonl` ledgers
([ADR 0021](0021-a-change-accepts-the-findings-it-causes.md)), the lint ceilings
`hack/lint/{no-throw,sdk-exports}.jsonl`, and rendering rows (markdown tables,
`rankRows`).

## Decision

Only the persistence layer says row. Buzz calls a store's repository through a typed
door and never learns that the store is a file of lines. The abstraction is the
per-store repository; no generic store module is added
([Abstract at the right layer](../design/principles/abstract-at-the-right-layer.md)).

### A repository owns its location and keeps its own persistence type

Each store is a repository value that owns its path; no caller computes a directory
for it. Each repository has two types, the way a protobuf handler does (Eli,
2026-11-07):

- the domain type in `types/`, one per concept, which is the boundary type: what the
  verbs take and return, what `boundary_types.go` mirrors into Buzz, what the CLI and
  SDK print;
- an unexported persistence type in the repository's package (for example `jobRow`),
  which carries the JSON encoding and what only the file needs (a header, a schema,
  unknown members kept for round-tripping).

The repository converts between them inline in the verb that reads or writes, with no
`toRow`/`fromRow` helpers: one conversion per direction where it is used, since there
is one domain type to convert to. The AGENTS.md convention "encoding lives on the entry
type" names the persistence type; a `types/` domain type carries no store encoding.

### The trail and marks become two repositories

`trail.Log` (events and blobs) and `trail.Marks` each own their location and read
through `file.ReadLines`, so a half-written last line is left unread instead of failing
the read, a corrupt line is an error naming `path:line`, and an unreadable trail is an
error, never "never ran" ([No silent defaults](../design/judgment/no-silent-defaults.md)).
`Rotate` becomes the log's own compaction, taken under `file.WithLock`, and stops being
a verb callers schedule. Marks stay an append-only history (a mark has no key), with
the supersede fold moved from each reader into the repository. The window and checkout
logic in `MagusTrailRead` moves into `trail.Log`, which also carries the request ref
`hack/retro/trace.buzz` reads from blobs today.

### `file.JSONL` grows to carry the job store

`file.JSONL[T]` gains what `job.Store` needs (Eli, 2026-11-07): an optional header
line, a delete that drops a key on the next compaction, a migration hook run under the
lock, and an append of changed entries only. Its fold keeps each key's
first-appearance position, which is what `job.Store.List` promises ("in the order they
were declared"); every store on `file.JSONL` changes its output order once to match.
`job.Store` then reads and writes through it and loses its own `jobsFile` parse and
write, and the landing-probe and unmerged-fact ledgers move onto it with a lock and
returned errors. `job.Store.Path()` stops being exported; gate waivers get their
location from the job store's directory, not from its file.

### A door is declared once

A door is one declaration: its Buzz signature and docs, the repository verb it binds,
its guard class (read or write) and its cost class. The Buzz declaration, the
binding, the dry-run mirror, the guard classification and the cost list are generated
from it. A door with no class is a write, so the guard fails closed. That closes a gap
measured on 2026-11-07: `clientReader.call` in `internal/guard/mcp.go` has no case for
`session` or `mark` and no default, so a client script that only writes marks is
judged as a bare `client` call, and `marks.jsonl` lives outside the checkout where no
write-path rule sees it. Whether a lower layer refuses that call was not traced; the
generated class makes the answer irrelevant.

The dry host (`internal/dry`) serves the playground, the docs examples, doctor's
per-target probes (MGS1049) and figure drawing; its rule is that a snippet that passes
there passes in a magusfile (`internal/dry/host.go:62-66`). Each declaration names one
dry mode from a closed set (Eli, 2026-11-07):

| mode | dry result | today's doors it covers |
| --- | --- | --- |
| `zero` | the zero value of the declared return | about 35 report and read doors |
| `echo` | the declared return with the named arguments copied in | `putJob`, `Job.exit`, `Session.mark`, `Graph.dir`, `Graph.layer`, the `change()` and `job()` constructors |
| `unknown` | the declared return with its own verdict or reason field set to unknown | `Graph.neighborhood`, `Job.register` (`base_verdict`) |
| `refuse` | an error naming the door and why it cannot run dry | `Change.tier` ("no checkout") |

`echo` copies the `id` and each option key that maps one-to-one to a field of the
return, generated from the input type's tags (`types.JobPut` for `putJob`); computed
fields (checkpoint, state, timestamps) stay zero. No Buzz caller a dry consumer
evaluates reads more: the one dry-evaluated caller, the `putJob` example in
`docs/guides/integrations/agents/leases.md:860`, discards the result, and `refuse`
would fail that example dry while it passes in a magusfile, which breaks the dry
host's rule (measured 2026-11-07, notes `adr-0030-open-answers.md`).

No mode is a default: the generator rejects a declaration without one, a write door
must name `echo`, `unknown` or `refuse`, and `zero` carries a stated reason
([No silent defaults](../design/judgment/no-silent-defaults.md)). No dry body is
hand-written. `Job.wait` returns a `JobStatus` instead of null, and a generated test
checks every door's dry value decodes as its declared return, extending
`TestMagusAPIMatchesBindings` (`internal/dry/host_test.go:72`). Prior art, as input:
kubectl's server dry-run echoes the object as stored, Terraform and Pulumi mark
server-computed values unknown, and a branch that needs one is refused. The forwarder
layer in `std/magus_handle.go` goes: the generated binding calls the repository verb.
A store gets a door when a Buzz caller first needs one.

### Write doors take boundary types

`putJob`, `Job.exit`, `Job.wait` and `Session.mark` take boundary types (`JobPut`, a
job result, `TrailMark`) instead of `map[string]any`, so a wrong field fails at check
time ([The checker catches it](../design/principles/the-checker-catches-it.md)). The
hand decoders (`decodeJobPut`, `decodeJobResult`, `decodeTrailMark`) are deleted.
The verbs stay as they are, including `Job.exit` with an empty result meaning abandon
(Eli, 2026-11-07).

### Two stores gain a Go owner

- The disk samples (`$XDG_STATE_HOME/magus/disk/samples.jsonl`) have two owners today:
  the shipped `std/lib/diskwatch.buzz` writes and compacts them, and Go's doctor reads
  the file. A Go repository becomes the one owner; `diskwatch.buzz`, `reclaimdisk.buzz`
  and doctor go through it, and the Buzz line code is deleted (Eli, 2026-11-07).
- `.magus/logs/failed-outputs.jsonl` is written by a shell loop in
  `.github/workflows/ci.yaml:836`. magus records failed outputs itself through a Go
  repository, the workflow step goes, and `hack/ci/show-failures.buzz` reads the door
  (Eli, 2026-11-07).

### Stores repo Buzz owns stay Buzz-owned

The mandate and attention stores are repo policy
([ADR 0019](0019-a-mandate-carries-out-decisions-already-made.md)); Go has no type for
them and gains none. Their line handling stays in `hack/policy/mandate.buzz`, in one
place: `issue-mandate.buzz` calls `mandate.buzz` instead of splitting the file. This is
the named exception to "Buzz never splits a store's lines": the owning module of a
repo-declared store does, and only it. The first draft of this record proposed a
`magus\store` host module for these stores; see Alternatives.

### Row stays below the boundary

"Job row" is persistence leakage, not a domain noun (Eli, 2026-11-07). Row is the word
for the unexported persistence type and the code that handles it, never for a boundary
type:

- no exported type, field, method or parameter in `types/`, the boundary registry or a
  Buzz mirror says row. `ConformanceRow` becomes `ConformanceThreshold` and
  `ConformanceReport.rows` becomes `thresholds` (Eli, 2026-11-07): `ConformanceKey` is
  already documented as naming "one threshold", a `ConformanceLimit` is the configured
  policy and a `ConformanceThreshold` is what it resolves to on this tree, with the
  distribution it was measured from. `ToolRow` becomes `Tool` (`HarnessTool` keeps naming a host's agent tools);
  `ModelRow` and the job store's `ModelPricing` become one type, `Model`; and each
  `{definition, rows}` field says what it holds, `ModelTiers.models` and
  `Grouping.groups` (Eli, 2026-11-07). `Model` sits beside `ModelTier`,
  `JobModelTier`, `ModelTiers` and a `Model` string field on `Job` and `JobPut`, so
  its doc comment says it is one model a host runs, not a tier. No domain type has
  row in its name;
- no text a person or a Buzz caller reads says row for a job: `magus job fork` help and
  flags, the guard's "fork the row" and "no row ... is in this workspace's job store",
  `magus\job: no row has id`, `is not a row id`, the Buzz docs and the SDK reference;
- the Buzz `jobRows` helpers in `hack/policy/helpers.buzz`, `hack/policy/guard.buzz`
  and `hack/retro/trace.buzz` say jobs;
- unexported persistence code keeps row.

This extends [ADR 0022](0022-domain-nouns-not-output-result-report.md), whose list
(Output, Report, Result) gains Row, and its nested-message lint gains the suffix.
ADR 0019 gains a dated amendment pointing here; its text stays as written.

### The store layer's defects are fixed with this work

Fixed as part of carrying out this record, not in a separate change (Eli, 2026-11-07),
because each fix lands in the code this record moves: the marks partial-tail failure,
the missing guard class for `Session.mark`, the `Rotate` race, the unlocked side-ledger
rewrites, `LastRun` and `ReadRecent` answering empty, the silent one-checkout fallback,
the dry-run `Job.wait` returning null, the archive name that collides within one second
(`internal/job/store.go:1007`), `Delete` removing the worktree before its write can fail
(`:1452-1470`), and an empty-id line read as the header (`:1387-1397`).

### The lint refuses a declared store path, not a `.jsonl` literal

The enforcement point fires on a hand-written `.buzz` file that names a path a store
declares, outside that store's owning module (`hack/policy/mandate.buzz` for the
mandate stores). The path list is generated from the repository declarations, so a
new store joins it when it is declared; a bare `.jsonl` never triggers it. Measured on
this tree, 2026-11-07, excluding `libs/gopherbuzz/**` and `gen/`:

| rule | files hit | true positives | precision |
| --- | --- | --- | --- |
| any `.jsonl` literal or magus state-dir read | 40 | 3 | 0.075 |
| only paths under a magus state dir | 12 | 3 | 0.25 |
| only a path a store declares | 3 | 3 | 1.0 |

The three true positives are `hack/retro/{cursor-audit,denials,trace}.buzz`; they move
onto the trail door before the lint denies. The sample is three files, so the 1.0 is
weak evidence; it clears the refusals' 0.8 floor on what exists. The rule misses a
store reached through a computed path or an environment variable (none on this tree).

### State

| item | State |
| --- | --- |
| `trail.Log` and `trail.Marks` repositories; readers on `file.ReadLines`; `Rotate` under the lock; window and checkouts out of `std` | proposed |
| `file.JSONL` gains header, delete, migration hook, changed-only append, first-appearance order | proposed |
| `job.Store` and its side ledgers on `file.JSONL`; `Path()` unexported | proposed |
| unexported persistence type per repository, inline conversion; AGENTS.md encoding rule reworded | proposed |
| doors declared once; binding, dry mirror, guard class and cost class generated; unknown door is a write; `std` forwarders deleted | proposed |
| every door names a dry mode (`zero`, `echo`, `unknown`, `refuse`); hand-written dry bodies deleted; `Job.wait` dry returns a `JobStatus`; a generated test decodes each dry value as its return | proposed |
| write doors take `JobPut`, a job result and `TrailMark`; hand decoders deleted | proposed |
| `hack/dev/bootstrap-{checkout,worktree}.buzz` call the jobs door instead of parsing CLI JSON | proposed |
| `hack/retro/{trace,cursor-audit,denials}.buzz` move onto the trail door with the request ref | proposed |
| `hack/dev/issue-mandate.buzz` calls `mandate.buzz` instead of splitting the mandate file | proposed |
| `hack/dev/{count-feedback,merge-job-branches}.buzz` fixtures build typed records | proposed |
| magus records failed outputs through a Go repository, replacing the `ci.yaml` shell step; `hack/ci/show-failures.buzz` reads its door | proposed |
| a Go repository owns the disk samples; `std/lib/{diskwatch,reclaimdisk}.buzz` and doctor go through it; the Buzz line code is deleted | proposed |
| `ConformanceRow` renamed `ConformanceThreshold`; `historymetrics` moves to `v1alpha2` | proposed |
| `ToolRow`, `ModelRow`, `rows` fields and every user-facing "row" renamed; ADR 0019 and 0022 amended | proposed |
| the store-layer defects listed above fixed | proposed |
| a Buzz lint refusing a path a store declares, outside that store's owning module; path list generated from the repository declarations; denies after the three `hack/retro` readers move | proposed |

### Deliberate break

Made on purpose, pre-1.0, with no shim, dual reader or alias
([Break now, hold still later](../design/principles/break-now-hold-still-later.md)):

- `ConformanceRow`, `ConformanceReport.rows`, `ToolRow`, `ModelRow` and the `rows`
  fields are renamed. The proto moves to `historymetrics.v1alpha2`, so `buf breaking`
  on `v1alpha1` stays green and the old package is deleted in the same change
  ([A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md)).
  If the rename stays inside `v1alpha1`, the change carries one
  `proto/ci.deviations.jsonl` line per buf finding, written by Eli.
- `putJob`, `Job.exit`, `Job.wait` and `Session.mark` take typed arguments; a script
  passing a map with an unknown field fails at check time.
- Job flags and JSON fields that say row are renamed; a script passing the old flag
  fails with the CLI's unknown-flag error.
- Every store on `file.JSONL` lists in first-appearance order.
- The trail's free functions and `job.Store.Path()` leave the exported Go API; the SDK
  lock (`hack/lint/sdk-exports.buzz`) records the change.

### Principles checked

- [Abstract at the right layer](../design/principles/abstract-at-the-right-layer.md):
  relies on it. The per-store repository is the layer that hides the file; the trail
  and marks gain one because they lack it, and a generic store module over all of them
  was rejected.
- [Everything is a file](../design/principles/everything-is-a-file.md): the file stays
  the store and stays readable; the decision enforces one owner and never-split-a-line
  in Go and Buzz, names one exception for repo-owned stores, and adds the enforcement
  point its Enforced by section lacks.
- [One source, every door](../design/principles/one-source-every-door.md) and
  [flags, config, help: one source](../design/principles/flags-config-help-one-source.md):
  a door is declared once and every copy is generated.
- [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md):
  the guard classifies a door from its declaration and fails closed, instead of a
  switch someone has to remember.
- [The checker catches it](../design/principles/the-checker-catches-it.md): write doors
  are typed.
- [No silent defaults](../design/judgment/no-silent-defaults.md): unreadable is an error
  on every repository and every door.
- [Delete before you add](../design/principles/delete-before-you-add.md): the forwarder
  layer, the hand decoders, the job store's own line handling, four trail readers and
  the CLI-text parsers go.

## Alternatives

### A generic `Repository[T]` and a `magus\store` host module

The first draft of this record: a five-verb `Repository[T]` over `file.JSONL`, a
descriptor generating a five-verb door per store, and `magus\store` plus a `libs/store`
Buzz module so repo Buzz could declare typed stores. Lost because it adds a primitive
the code base does not have, hides nothing a per-store repository does not, and
flattens domain verbs (`Claim`, `Settle`) into `update` (Eli, 2026-11-07). Spike
evidence for the repo-declared half (`magus buzz` runs, 2026-11-07): a protocol cannot
require a static `decode`, a generic cannot be constrained to a protocol (type
parameters are erased), and a Go host method cannot be generic over a Buzz type, so
the door would have needed `decode`, `key` and `encode` closures and a Buzz wrapper.
Reopens if many repo-declared stores appear and each grows its own copy of the line
handling.

### One trail repository holding events, blobs and marks

Lost because marks are a person's verdicts with their own lifecycle and file, and the
events are a ring the log rotates; one value would carry two compaction rules. Reopens
if marks start being written by the trail's own hooks.

### Keep `job.Store` off `file.JSONL` as a named exception

Lost (Eli, 2026-11-07): two folds with different orders and two partial-line policies
is the duplication the review found, and the header, delete and migration needs are
general enough for the primitive. Evidence: `job.Store.parse` keeps first appearance,
`JSONL.fold` last; both use `file.ReadLines` and `file.CompactDue` already. Reopens if
the grown `file.JSONL` cannot express the job store's retention pass inside its lock.

### Only make the guard fail closed, keep five hand-written door copies

Lost because the dry-run mirror and cost list drift the same way the guard switch did
(the dry `Job.wait` already returns null). Reopens if generating the dry mirror proves
impossible for a door whose dry result depends on run-time state.

### Dry behavior as zero plus an optional hand-written function

Lost because about six hand-written dry bodies stay, free to drift from the
declaration as `Job.wait` did, and the default is the quiet empty that No silent
defaults warns about. Evidence: `internal/dry/host.go`, 2026-11-07 (research notes
`dry-door-shape.md`). Reopens if a door's dry result needs computation no mode can
state.

### An explicit unknown marker in every boundary type

Lost because it changes gopherbuzz and every generated mirror, and doctor's probes
would stop at the first read of `.indexed` or `.drifted` instead of tracing the body.
Reopens if `unknown` as a mode proves too coarse for a type with no verdict field.

### Split `Job.exit` into exit and abandon

Not taken (Eli, 2026-11-07): typed inputs fix the checker gap; the verb set is left as
it is. Reopens if a caller abandons by accident through an empty result.

### Move the mandate stores into Go

Lost because ADR 0019 puts mandate policy in repo Buzz. Reopens if a second, non-repo
caller needs the mandate records.

### A lint on any `.jsonl` literal

Lost on precision: 3 of 40 files it fires on reach a magus store (0.075, 2026-11-07),
far under the refusals' 0.8 floor; the rest are foreign transcripts, committed
ledgers, lint ceilings, fixtures and diagram labels. Reopens never on its own; the
declared-path rule replaces it.

### `refuse` as putJob's dry mode

Lost because the docs example that calls `putJob` would fail dry and pass in a
magusfile, against the dry host's rule. Reopens if a dry consumer starts branching on a
computed field of the put's result.

### Rename the Row types and stop there

Lost because it fixes the word and leaves the CLI-text parsers, the trail readers and
the untyped doors. It is a subset of this decision.

## Consequences

- The listed Buzz files change; any outside magusfile using the Row types, passing
  maps to the write doors or using a job flag that says row breaks at check time or at
  the CLI.
- About 30 Go callers of the trail functions move to `trail.Log`; `internal/guard`
  stops reading `job.Store.Path()`.
- Stores on `file.JSONL` change list order once.
- The mandate module remains the one Buzz module that splits a store's lines, and the
  lint names it.
- No new Go package or host module is added; `file.JSONL` and `internal/trail` grow.
- Foreign JSON Lines, the deviations ledgers and the debt stores named in
  [Everything is a file](../design/principles/everything-is-a-file.md#debt-and-exceptions)
  stay as they are.

## Open questions

None. The questions this record carried were answered on 2026-11-07 and folded into
the Decision: the dry `echo` for write doors, the lint's scope, the `Model` name and
the guard gap. The number is rechecked against the base at merge: on 2026-11-07 main
was d29cea148 with 0027 its highest, 0028 and 0029 were claimed on other branches
(0029 twice), and no ref but this one claimed 0030.
