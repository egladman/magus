---
title: "ADR 0050: the harness is a module named safetyscissors"
order: 50
description: "magus's harness layer becomes its own Go module, safetyscissors. The layer is guard verdicts, job leases and goal grading, the trail, sessions, the sandbox policy, the build budget and the model tier table. A Go program imports it with no magusfile, no magus.yaml, no magus binary, no workspace and no version control. It stays in the magus repository as a nested module, and magus becomes its first client. The name says what the library is for: the tool still works, and you cannot hurt yourself with it, whoever is holding it. It ships no agent loop, no model client and no router. Steps 1 to 4, 6 and 7, the budget, the verdict and step types, the tier set, the JSON Lines helpers, the session store and the secrets module, are built, and so are step 5's trail event and readers and step 8's job types."
tags: [adr, decision, sdk, harness, guard, jobs, module]
status: proposed
date: 2026-11-15
---

# ADR 0050: the harness is a module named safetyscissors

## Context

On 2026-11-15 Eli asked whether a Go developer could use magus's harness on its own, in
place of an agent framework like Mastra, without the magus binary, a magusfile or a
workspace. The question was whether that use is clean today, what it is missing, and
whether the layer should carry a name that is not magus's. The evidence below was read
from this tree at 8a1625c85 on 2026-11-15. A scratch module outside the workspace
imported magus through a `replace` directive and ran the probes with plain `go`.

### What a stranger can reach today

Every harness door goes through the whole workspace:

- The guard is reachable only as `(*Magus).JudgeCommand`, `JudgeWrite` and `Judge`
  (`judge.go:29-55`). That needs `magus.Open`, and `Open` composes config, project
  discovery, VCS, the cache, the broker and telemetry.
- Jobs are reachable only through `std.MagusPutJob`, `MagusExitJob` and `MagusWaitJob`,
  which are Buzz bindings. A Go caller has to find and call the undocumented
  `types.WithWorkspace(ctx, m)` first, or gets "no workspace on the context"
  (`std/magus.go:1558-1568`).
- A trail entry is written only as a side effect of judging; there is no write door.
  The trail can be read through `std.MagusTrailRead`, which also needs a workspace on
  `ctx`.
- Sessions, secrets, the limiter, hook wiring and the MCP tools are in `internal/`, with
  no door at all.
- `internal/agent/harness` is an end-to-end test driver for agent hosts, not a library.
- The cleanly importable pieces are the `types/*` data types and the model tier table
  in `types/agent`, which has no internal imports.

### What the probes measured

- `import _ "github.com/egladman/magus"` pulls in 751 packages:
  - 91 internal packages;
  - 29 packages from `libs/*`;
  - 341 packages from 61 third-party modules, among them gRPC, connect, the full
    OpenTelemetry SDK and its OTLP exporters, `mvdan.cc/sh`, goldmark and x/tools.
- The guard probe binary was 76 MB.
- The guard package alone reaches 531 packages, 75 of them internal, from 40 modules.
- In a temp directory with no magusfile and no git, the guard judged `rm -rf /`,
  `curl … | sh` and a read of `~/.ssh/id_rsa` as pass. After `git init`, `rm -rf /` was
  denied, but by the `worktree-remove` rule, which protects a worktree. It is not a
  general rule against destructive commands. With no workspace, the guard protects
  almost nothing.
- Judging wrote `.magus/` into the judged directory: `activity/events.jsonl`, advisories,
  `guard-settings.jsonl` and `workspace.cache.json`.
- A job could be put, exited and graded only after three things:
  - `git` was installed;
  - the directory held a committed base revision;
  - the job's work was committed.

  The row went to the user's global `$XDG_STATE_HOME/magus/jobs/<dir>-<hash>`, because
  the store resolves its directory through `vcs.StateDir` (`internal/job/store.go:267`).

### Why it is coupled

Non-test Go source counted on 2026-11-15:

- `types`, at 24,918 lines, holds the job, guard and trail types and imports `spells`.
  No package below it can leave until those types do.
- `internal/guard`, at 51,845 lines, imports `internal/workspace`, `internal/interp`,
  gopherbuzz, `internal/commands`, `cmd/magus/gen`, config, the project graph, the cache,
  the broker and VCS. `Judge` (`internal/guard/guard.go:766`) runs the 137 built-in rules
  directly, so they cannot be separated from it. The parts that belong to the harness
  itself are:
  - the verdict merge;
  - the step table (`internal/guard/step.go`, the ADR 0046 shape);
  - the built-in settings (`internal/guard/builtin/builtin.go:521`).
- `internal/job`, at 23,431 lines, reads config in 11 files and VCS in 8. It also has
  ambient dependencies:
  - it reads `os.Getenv`, `os.Getwd` and `os.Stderr` directly;
  - it sets a process-wide `SetCheckGrader`.

  The job store, lock, authorization, grants, lifecycle and verification are the core.
  Worktrees, checkouts, landings, merges and plans belong to magus.
- The trail (3,124 lines), sessions (3,635), sandbox (2,771) and secret (1,276) packages
  each depend on VCS, config or spells at a few named sites. The limiter
  (internal/cache/limiter.go) imports nothing from magus.

### What else exists

A survey on 2026-11-15 compared Mastra, LangGraph, the OpenAI Agents SDK, the Claude
Agent SDK, Google ADK for Go, Eino, Genkit Go, NeMo Guardrails, Invariant, AWS AgentCore
Policy, StrongDM Leash and Microsoft's Agent Governance Toolkit:

- An allow-or-deny verdict on a tool call plus an audit log, in Go, already exists.
  Microsoft's toolkit ships a Go module with default deny, a denial returned as a value,
  and hash-chained audit entries. OPA, Cedar and the MCP gateways fill the rest.
- None of them offers work leases bound to write paths with an overlap check between
  workers, goals graded by a check rather than a model, or a trail that ties verdict,
  lease and grade to one job row naming no model. Those answer "who owns this change, and
  is it finished?". That is the part of magus worth taking elsewhere, and it is what
  [A harness is not an agent thing](../design/principles/a-harness-is-not-an-agent-thing.md)
  already says: the same checks hold a person and an agent.

## Decision

### 1. The harness layer is its own module

The harness becomes a nested Go module in this repository, as gopherbuzz and
diagnostics already are. Its path is `github.com/egladman/magus/libs/safetyscissors`. A
vanity path waits until the module leaves the repository, if it ever does. Its packages,
each with one job:

| Package   | Holds                                                                                                            |
| --------- | ---------------------------------------------------------------------------------------------------------------- |
| `verdict` | `Decision`, `Verdict`, `Judgment`, the stricter-of merge, `DeniedError`                                          |
| `step`    | the step and its declared rows (ADR 0046), `Rule`, `Table`, `(Table).Judge`                                      |
| `job`     | the job store opened on a directory, `Job`, write paths, goals, fork, exit, grade, `Covers`                      |
| `trail`   | append-only events: read through the package, written only by the module's own packages                          |
| `session` | session ids and their append-only records                                                                        |
| `sandbox` | read, write and exec policy, narrowed to a lease                                                                 |
| `budget`  | the slot limiter, machine claims and their memory curves, and a spend limit priced from rows the caller supplies |
| `tier`    | the model tier set and price table built from rows the caller supplies                                           |

Secrets are not a package here. Resolving them, redacting them from what is recorded and
forwarding one to a child process live in their own module, `libs/secrets`, which magus
and the module's trail both import; decided by Eli on 2026-11-16.

The root package holds only the package doc. Call sites read `verdict.Deny` or
`job.Covers`, never a themed name.

This decision puts the trail's writer in `libs/safetyscissors/internal`, so only the module's own
packages append to it, and the public `trail` package exports reading only. The writer
moves with the guard, which is the first of the module's packages to record anything. magus keeps
its own events in its own trail. This is part 7's refusal of a caller writing the trail,
made a package boundary; decided 2026-11-16 by the build, Eli to confirm.

The Buzz-facing names stay as they are: a magusfile reads the same bindings after each
step, and only Go spellings move; decided 2026-11-16 by the build, Eli to confirm.

### 2. It runs with nothing but a directory

- A caller opens every store on a directory it names. Nothing resolves a repository
  identity, a user state directory or a working directory on its own.
- Nothing reads the environment or writes to stdout or stderr. Each of those is an option
  the caller passes.
- Nothing is process-global. The grader `SetCheckGrader` sets today becomes an option on
  the store.
- The core imports the standard library, `golang.org/x/sync`, `golang.org/x/sys`,
  `github.com/bmatcuk/doublestar/v4` and the repository's own `libs/*` modules, and nothing
  else; decided 2026-11-16 by the build, Eli to confirm. A test in the module (`doc_test.go`) fails on any other direct
  requirement in its `go.mod`, and testify is allowed there for the tests alone. The file
  lock is `x/sys` (`flock`, `LockFileEx`) in place of `gofrs/flock`, and JSON Schema
  generation (`google/jsonschema-go`) stays on magus's side. OpenTelemetry, if it comes,
  is a separate sub-module.
- The module needs no `GOEXPERIMENT=jsonv2`: it encodes with `encoding/json` and leaves
  `<`, `>` and `&` unescaped, the bytes magus's v2 codec writes; decided 2026-11-16 by the build, Eli to confirm.
- No VCS is required. A grade that needs to know what changed takes it from an observer
  the caller supplies; magus's observer reads VCS.
- A step table with no rule is a construction error. The guard passing `rm -rf /` in an
  empty directory today is a silent default; this replaces it with a refusal that names
  what is missing.

### 3. Policy is declared rows plus Go rule functions

This exposes the shape ADR 0046 built (`WithStepSpecs`, `WithStepRule`, the step table):

- Rows declare each kind as data.
- A rule is a Go function from a request to a verdict.
- Buzz stays magus's own way of writing policy. magus adapts each Buzz rule into a
  `step.Rule`, the shape `steps.Rule` already has (`internal/guard/steps/steps.go:16`).

Every built-in rule ships in the module, and none applies by default. A caller opts in
by passing each rule, or a named group of them, as an option to the table. A rule that
reads magus's world (worktrees, `magus run` routing, approved sources, the magusfile)
takes what it reads from a seam the caller supplies, and the table refuses to build
when the rule is passed without it. An empty table is still a construction error
(part 2), so opting in to nothing fails loudly instead of allowing everything.
`AllRules()` makes "everything" a call the caller writes, never what nil means.

The shape is `go/analysis` with `multichecker.Main`. Every analyzer ships as a value,
none runs unless the program passes it, and a group is a plain slice. `crypto/tls` and
`crypto/x509` fit worse, because nil there means defaults the library picks.

Today the 137 rules (99 deny, 31 advise, 7 off; `./magus describe rule`) are inline arms
in `evaluateLine` (`internal/guard/shell.go:2758`) and `Judge`, so each becomes a value
first. Sorted by what they read, as proposed on 2026-11-15 and not traced rule by rule:

| Group        | Rules | Reads                                 | Seam the caller supplies      |
| ------------ | ----: | ------------------------------------- | ----------------------------- |
| `shell`      |    14 | the step                              | none                          |
| `vcs`        |    11 | the step and VCS state                | a VCS reader                  |
| `files`      |    11 | the step's path and declared outputs  | the declared outputs          |
| `reads`      |     9 | the step and a symbol index           | an index reader               |
| `lease`      |    25 | the lease and the job row             | none: the job store moves     |
| `delegation` |    27 | the session, the spawn input, tiers   | none, except four magus rules |
| `person`     |     7 | magus CLI verbs a person must run     | magus's command tree          |
| `magus`      |    25 | the command tree, spells, the run log | magus's command tree and runs |
| `guard`      |     8 | the guard's own state                 | two are the table's own       |

### 4. What the caller owns

- Prices. The module ships no model, provider or token price. A caller that wants a
  spend limit passes its own price rows, and the budget refuses the step that would
  cross it. A price that changes never waits on a release.
- The graph. The module computes no project graph and imports none. A caller that wants
  owners or dependents in a rule implements the reader the rule asks for, from magus's
  graph, a JSON file built ahead of time, or anything else.

### 5. magus is the first client

`judge.go` builds a `step.Table` and registers magus's built-ins and Buzz adapters as
rules. `internal/job` keeps worktrees, checkouts, landings, merges, plans and outcomes on
top of the module's job store. `vcs.StateDir` computes the directory magus passes to
`job.Open`. The CLI, the hooks, `magus agent install`, the MCP server and the
workspace's rules stay in magus. The module never imports magus.

### 6. The seams

Only seams that already have a second implementation become interfaces:

- `job.Reader`: the module's store, and an embedder's own rows (`types/job_reader.go:8`).
- `job.Observer`: magus's VCS-and-output reader, and a caller's own check result
  (`internal/job/verify.go:36`).
- `secrets.Provider` in `libs/secrets`: the environment, and magus's spell-backed
  provider (`internal/interp/bindings/secret_provider.go`).

Repository identity becomes a directory string, not an interface. Each seam gets one
option. The module's tests carry their own fakes: it never imports `libs/testkit` or the
root module, so a fake lives beside the test that needs it; decided 2026-11-16 by the build, Eli to confirm.

### 7. What it refuses

These stay out, as the
[refusal of an agent loop in the SDK](../design/refusals.md) and
[the Go SDK reference](../reference/go-sdk.md#what-the-sdk-leaves-out) already say:

- an agent loop;
- a model client, prompt or message types;
- model routing or automatic escalation;
- retries, memory, retrieval or a workflow engine;
- rules or evals judged by a model;
- trust scores;
- a hosted gateway or an account;
- default allow, or auto-approve;
- anything that rewrites what reaches the model;
- a caller writing the trail directly. The library records what passes through its own
  calls, and an entry nobody witnessed would taint every other entry;
- price tables or provider settings it would have to keep current;
- a project graph, embeddings or retrieval.

The names carry no agent vocabulary, because the mistakes it prevents are the ones a
person makes too.

### 8. The name is `safetyscissors`

Safety scissors cut. You cannot stab yourself with them, whoever holds them. That is the
library's purpose stated as an object, and it says nothing about agents.

- On 2026-11-15, neither pkg.go.dev nor GitHub had a module or repository named
  `safetyscissors`.
- The theme is deliberately its own, apart from magus's magic vocabulary, so the name
  does not read as a magus internal.

Eli picked it on 2026-11-15.

### 9. Extraction order

Each step merges on its own and leaves magus green. Eli approved building every step on
2026-11-15, ahead of the open pull requests merging.

| Step | Moves                                                                                                                                           | Size                          | Needs Eli's call                                                                                                                       | State                      |
| ---- | ----------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- | -------------------------- |
| 1    | `budget`: internal/cache/limiter.go and its slot watch, the machine budget, the claim types and the memory curve and sizing types they carry    | ~2,000 lines                  | no; breaks the Go API lock                                                                                                             | done                       |
| 2    | `verdict` and `step` types out of `types/guard.go`, types/guard_judgment.go, types/agent/step.go                                                | ~700 lines                    | yes: breaks the Go API lock                                                                                                            | done                       |
| 3    | `tier`: types/agent/model.go, `internal/agent/model`, `TierCeiling` (`register.go:111-117`)                                                     | ~650 lines                    | no                                                                                                                                     | done                       |
| 4    | the JSON Lines store helpers the module's stores need, copied into `internal/jsonl` with `types/enum` into `internal/enum`; magus keeps its own | ~500 lines                    | no                                                                                                                                     | done (now `libs/codec`)    |
| 5    | `trail` core; feedback marks, replay and env propagation stay in magus                                                                          | ~1,500 lines                  | no                                                                                                                                     | done (now `journal`)       |
| 6    | `session`, with the directory supplied by the caller                                                                                            | ~2,000 lines                  | no                                                                                                                                     | done                       |
| 7    | secrets into their own module, `libs/secrets`, from `internal/secret` and types/secret.go                                                       | ~1,300 lines                  | no                                                                                                                                     | done                       |
| 8    | the job types out of `types`, with no alias, and the generated Buzz mirrors regenerated                                                         | ~5,000 lines                  | yes: 16+ importers                                                                                                                     | in progress (mirrors owed) |
| 9    | the `job` store core, with config as `Limits`, VCS as a directory, env and stderr as options                                                    | ~8,000-10,000 of 23,431 lines | no: `jobs.jsonl` stays where `vcs.StateDir` puts it, and magus passes that directory (decided 2026-11-16 by the build, Eli to confirm) | done                       |
| 10   | the guard engine: the step table and settings, with magus's 137 built-ins registered as rules                                                   | the largest                   | yes: `Judge`'s structure changes                                                                                                       | planned                    |
| 11   | `sandbox`, after step 9, with its own grant type replacing `spells.Sandbox`                                                                     | ~2,800 lines                  | no                                                                                                                                     | done                       |

Sizes are estimates from the line counts above, not measured diffs.

Step 1 moved more than its row first named. A claim carries its memory curve and sizing,
and a snapshot carries its waiters and recorded admissions, so `types.MemoryProfile`,
`MemoryCurve`, `MemorySizing`, `SlotSizing`, `FormatMB`, `MachineWaiter` and
`MachineAdmission` moved with the claim. The limiter took its slot watch and the one
context value that says what a step holds, and a refusal of a wedged pool is a
`budget.SlotDeadlock` that magus turns into MGS3013 through `budget.WithRefusal`. The
machine budget learns which invocations only wrap a command from `WithWrappers`, since
the wrapper id stays magus's. Each name lost its `Machine` or `Memory` prefix
(`budget.Claim`, `budget.Profile`), with no alias, so the Go API lock records a break.

Step 2 moved the types and nothing that judges with them. A judgment's remedies are
`verdict.NextStep`, field for field `types.NextStep`, since the module cannot import
magus's breadcrumb; the guard converts between the two where a deny is shaped. Its
`LeaseFrom` is a string until the job types move in step 8. The envelope version is
`verdict.APIVersion`, typed by `libs/apiversion`, and `agent.GuardAPIVersion` names it.
`ValidateGuardRuleName` is `verdict.ValidateRuleName`, with its own copy of the kebab-case
suggestion. The Buzz names stay through `.buzzgen.yaml` (`GuardVerdict`, `GuardDecision`,
`magus\agent\StepRequest`).

Step 3 moved the tier set with the job's record of it and the price table. A job type
embeds the assignment and the module may not import `types/agent`, so `JobModelTier`,
`SkillVariant` and `ModelTierSource` moved too, as `tier.Assignment`, `SkillVariant` and
`Source`. `types.ModelPricing`, `PriceStep` and `USDPerMTok` are `tier.Pricing`,
`PriceStep` and `USDPerMTok`; `Table` and `InEffect` hold only rows the caller passes.
Pricing a session's calls stays in magus as `model.PriceContext`, since it reads magus's
call facts and billing. `TierCeiling` is `tier.Ceiling`, which refuses with
`*tier.AboveParentError` or `*tier.IncomparableError`; the job store gives those MGS3055
and MGS3056, since the module cannot name magus's codes. Each name lost its `ModelTier`
prefix, with no alias. The Buzz names stay through `.buzzgen.yaml`.

Step 5 moved the trail's event and its readers, and no writer. `trail.Event`, `Phase`,
`Kind`, `ReadRecent`, `ReadBlob` and `ValidRef` left `internal/trail`, and the guard
trail's `Section`, `Verdict`, `Trail`, `Observation`, `Spawn` and `Mark` left
`types/trail`, which is gone. Every reader takes the directory the caller names and reads
`activity/` beneath it, the layout magus's writer keeps. Every magus producer writes the
trail today, and none of them is a module package yet, so `Append`, `WriteBlob`,
`Rotate`, `StampOrigin` and the agent-hook helpers stay in `internal/trail` over the
module's `trail.Event`. The writer moves into `libs/safetyscissors/internal` with the
guard, and until then the module exports no door to write. Reading redacts nothing, so
the module does not import `libs/secrets` yet. `KindCredentialGrant` is `KindGrantIssued`,
so the name no longer reads as a credential to gosec, and the value on disk is unchanged.
A refused ref is a `*trail.InvalidRefError`. The module's `trail.Event` reads only
`duration_ms`; magus decodes its own older `dur_ms` events in `internal/trail`, under
that reader's `compat(until:)`. Feedback marks, replay, propagation, consult, mint and origin stay in
magus. The Buzz names stay through `.buzzgen.yaml`, and no alias remains.

Step 6 moved the session store's ids, record writer and reader, fold and pruning into
`session`, opened on a directory the caller names. `vcs.StateDir` and `xdg.StateDir` stay
in magus as `sessions.Dir`, and so do attention, gates, checkpoints, advice, contact,
the prompt cache and the adapters. Pruning keeps an attention request whole through a
`session.Tie` magus passes with `WithTie`, since the queue that decides a request is open
stays in magus; a payload that bounds itself is a `session.Bounder`. Minting an id needs
process state, so `NewID` stays in magus and the module only checks one with
`ValidFileID`. Nothing in the module assumes a host session: an editor pane names its
own invocation. The clock is `WithClock`, the records are written through
`internal/jsonl`, and no alias remains.

Step 7 moved secrets into `libs/secrets`, a module of its own rather than a
safetyscissors package, so magus and the module's trail share one. It holds `Secret`,
which was `types.Secret`, `Grant`, which was `types.SecretGrant`, the `Resolver` and its
`Provider`, `Redact`, `RedactString`, `NewRedactingHandler` and the endpoint forwarder,
and `internal/secret` is gone. The module keeps no global and writes nowhere: the
spell-backed provider is `secrets.WithOpener(bindings.SecretOpener)`, which cmd/magus
passes through `magus.WithSecrets`; the forwarder logs through `WithLogger`; and a value
too short to mask is reported through `WithShortSecret`, where
`journal.NewSecretResolver` logs MGS2011. A refused grant is a `*secrets.GrantError`, which
magus gives MGS1027. `OpenEndpoint` takes its scope as an argument, and magus passes the
run's invocation id, so the context key that carried it is gone. No alias remains.

Step 8 moved the job record types into `job` and left every rule that names an MGS code
in magus. A record's own validation returns `job.Fault` values with a kind and no code,
and `types.DeclarationProblems` gives them MGS3037 or MGS3038. A deviation may permit only
a code the caller passes, so `Permissible` and the problem types stay in `types`. `Origin`
took the credential and its access grant, as `job.Access`, since `job.Grant` is a job's
rule grant. `JobAttempt` is `job.CapturedRun` beside `job.Attempt`. `Schema` is the
module's own copy versioned by `libs/apiversion`, and the version constants are untyped.
The agent section left `types/agent`, which is gone. The Buzz names stay through
`.buzzgen.yaml`, and the generated mirrors are regenerated after the move.

### Principles checked

- [A harness is not an agent thing](../design/principles/a-harness-is-not-an-agent-thing.md):
  the module is the same checks for whoever holds the tool. The name and the package
  names say so by mentioning no agent.
- [The harness sits on the tool layer](../design/principles/the-harness-sits-on-the-tool-layer.md)
  and [the outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  the module judges, leases, records and grades. It never runs, edits or routes the
  caller's loop.
- [What we do not build is the product](../design/principles/what-we-do-not-build.md):
  part 7's refusals are the reason the module exists beside the agent frameworks, not a
  limit on it.
- [Delete before you add](../design/principles/delete-before-you-add.md) and
  [Abstract at the right layer, or not at all](../design/principles/abstract-at-the-right-layer.md):
  every package moves code that exists. The three interfaces each have a second
  implementation today, and repository identity is a string, not an interface.
- [No silent defaults](../design/judgment/no-silent-defaults.md): the empty-directory
  pass on `rm -rf /` is the defect part 2 removes. An empty table is a construction error.
- [Everything is a file](../design/principles/everything-is-a-file.md): every store stays
  append-only JSON Lines on a directory the caller names.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md) and
  [the Go proverbs](../design/principles/the-go-proverbs.md): a deny is a value with a rule
  and a reason; the error is for a judgment that could not be made.
- [Worth cloning](../design/principles/worth-cloning.md): the module is the part of magus
  that is better when copied, published as a library so it can be taken without the rest.
- [Break now, hold still later](../design/principles/break-now-hold-still-later.md) and
  [a deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  steps 2, 8, 9 and 10 break exported names and stored paths with no alias. Each records
  its break and merges green.

## Alternatives

### Keep the harness behind `magus.Open`

- Why it lost: it works only by building a whole workspace. It pulls 751 packages and
  writes `.magus/` and global state the caller did not ask for. Without a workspace the
  guard protects almost nothing.
- Evidence: the probes in Context, 2026-11-15.
- What would reopen it: a `magus.Open` that composes nothing but the harness, at a
  fraction of the dependencies. Getting there is this extraction under another name.

### Move the internal packages out of `internal/` as they are

- Why it lost: exporting `internal/guard` exports its 137 transitive magus packages and
  its `Dependencies` struct, which carries `types.WorkspaceRepository` and
  `[]*spells.Spell` (`internal/guard/guard.go:46`). The coupling is the problem, not the
  directory name.
- Evidence: the import counts in Context.
- What would reopen it: none while the guard imports the workspace.

### Its own repository now

- Why it lost: magus is the only client, and every extraction step changes both sides
  at once. One repository keeps each step a single green change. Eli ruled on
  2026-11-15 that it stays in magus indefinitely.
- Evidence: steps 2, 8, 9 and 10 each touch the module and magus together.
- What would reopen it: a second client, or contributors with their own release cadence.

### Policy as a Buzz file without a workspace

- Why it lost: it puts gopherbuzz and the interpreter into the module, and an approved
  copy of a rule needs a VCS to read it from (`internal/guard/workspace_rules.go:581-599`).
- Evidence: the guard's imports of `internal/interp` and gopherbuzz.
- What would reopen it: a Buzz runtime small enough to be an optional sub-module, with
  approval that does not need VCS.

### Policy as a data-only rule language

- Why it lost: it would be a second rule language beside Go and Buzz. Cedar and OPA
  already exist for anyone who wants one.
- Evidence: the survey in Context.
- What would reopen it: users asking to share one policy across Go and another language.

### An agent framework

- Why it lost: an agent framework like Mastra is refused, and it would compete where the field is crowded while
  dropping the leases and grades no one else has.
- Evidence: the refusal of an agent loop in the SDK; the survey in Context.
- What would reopen it: nothing; this is the line the project exists to hold.

### Other names

Each was weighed on 2026-11-15:

- `because`: a stopword, so it cannot be searched.
- `harness` and `gopherharness`:
  - Go developers read "harness" as a test harness first;
  - Harness Inc. owns the word in CI/CD;
  - `gopher` + X reads as "a Go port of X", which fits gopherbuzz but not this.
- `leash`: StrongDM's product, and about eight governance modules on pkg.go.dev.
- `bridle`: an active module shipping Claude Code providers.
- `creance` and `checkrein`: free, but not words a reader recognizes.
- The magic and legal names: they read as magus internals, or as a brand.
- `bumpers` and `waterwings`: free on pkg.go.dev and liked. They describe the
  deflection and the floating, where `safetyscissors` describes the purpose.

What would reopen it: a collision appearing before the first tag.

## Consequences

- `types` shrinks by the guard, job and trail types, and every importer and the generated
  Buzz mirrors change spelling with no alias.
- The Go API lock records breaks at steps 2, 8, 9 and 10.
- `Judge` stops hard-wiring its rules. The built-ins become registered rules, and
  magus's guard behavior must not change; the guard's existing tests are that check.
- The job store's directory comes from the caller. Rows stay under
  `$XDG_STATE_HOME/magus/jobs`, where `vcs.StateDir` puts them, and magus passes that
  directory; decided 2026-11-16 by the build, Eli to confirm.
- The module is tagged as `<module path>/vX.Y.Z`, as the other nested modules are, and
  gets its own Go API lock.
- [The Go SDK reference](../reference/go-sdk.md) changes its "the harness is CLI-only"
  line once step 10 merges.
- `internal/agent/harness` keeps its name. It is magus's host test driver and never
  enters the module.

## Open questions

Prototypes for the second, third and fourth questions were built on 2026-11-15 outside
the repository, in Go with only the standard library, and pass `go test -race`. None is
decided.

- The rule groups in part 3 are proposed from each rule's catalog text. Nobody has
  traced what each rule reads, and the four delegation rules that carry magus content
  have no seam yet.
- What an `ask` verdict does for a caller that can route the question somewhere. Today
  an ask becomes a deny unless the host renders it (`internal/guard/guard.go:1461-1466`),
  and the answer is never recorded. The prototype compared three shapes:
  - a question token answered by a second call, which ties the approval to no step;
  - an asker passed at construction, which blocks judging until an answer comes and cannot span
    the hook's two processes;
  - judging the same step again with the answer attached. The answer counts only for
    the step it was asked about, under the same policy, before a required time-to-live
    runs out, and it is used up by the judgment it clears. This is the lean, because the
    answer reaches the trail only through judging.

  The answerer is whoever the caller routes the question to, and the library cannot
  tell which. Most often it is another model, sometimes a person, sometimes a
  script. An answer means only that something the caller chose said yes, never that a
  person approved. The record keeps the answerer's label as given and calls it
  unverified, and nothing in the module's names or messages implies a human.

  An answer carries a basis, as a grade does, so the record can say a person approved
  only when that is proven. Eli added this on 2026-11-15:
  - reported: the answer and its answerer's label, as given. This is the default.
  - signed: the answer is signed by a key held by an approval tool outside the worker's
    reach, such as a phone prompt, a chat approval or a hardware key. The caller passes
    the verifier when it builds the table, and the record names the signer the verifier
    returns. A signed answer is bound to the question's id and the step's digest, so it
    cannot be replayed onto another step.

  A rule may require a signed answer. A reported answer to that rule's ask is refused
  and recorded, so a model's yes never clears it. A rule that requires one, built into a
  table with no verifier, is a construction error. Signed answers have not been
  prototyped; the grading prototype's `Attest` path is the shape to reuse.
- Grading a goal from a caller's own check. A result the caller hands over is evidence
  the worker could author, so the lean is three bases, each named on the grade:
  - witnessed: the caller passes its unstarted `exec.Cmd`, and the library checks the
    argv, hashes the tree, digests the output and reads the real exit status. It still
    starts nothing.
  - attested: a statement signed by a key outside the worker's reach, such as CI.
  - reported: the caller's word, accepted only by a goal that opts in to it.

  A goal that accepts no basis is a construction error. A witnessed run still trusts
  the `go` binary on `PATH`, and the prototype's trail is an in-memory hash chain, not
  yet the `.jsonl` store.

  Built on 2026-11-17 in `libs/safetyscissors/job`; see the amendment of that date.
- Sandbox confinement off Linux. Landlock confines files only, and network is
  unconfined (`internal/sandbox/landlock_linux.go:75-79`). The lean:
  - macOS runs the child under `/usr/bin/sandbox-exec -p` with a profile generated from
    the same rules, as Codex and Claude Code's sandbox runtime do. That needs no cgo.
    `sandbox-exec` is deprecated and its profile language undocumented, and Apple names
    no replacement.
  - Windows starts unsupported. Restricted tokens, job objects and AppContainer are
    reachable from `golang.org/x/sys/windows`, at several weeks of work each.
  - The module's default mode is `required`. A platform with no confinement fails loudly
    and names the opt-out, instead of today's notice and an unconfined child. magus's own
    config default is unchanged; decided 2026-11-16 by the build, Eli to confirm.

  Built on 2026-11-17 in `libs/safetyscissors/sandbox`; see the amendment of that date.
- The record's new API group. Job rows carry `batch.magus/v1alpha1` today
  (`internal/job/store.go:338`). It stops naming magus everywhere it is written, and the
  string is still to pick.

## Amendments

### 2026-11-17: the root package holds `New` and `SafetyScissors`

Part 1 said the root package holds only the package doc. It now holds one constructor,
because every consumer stitched three things by hand: one directory for the job store,
the session writer and the trail; a lease narrowing a child's sandbox; and a spawn judged
against its parent's model tier. buzzed, the first consumer outside magus, imported
`verdict` alone rather than write that wiring. Asked for by Eli on 11-17.

- `safetyscissors.New(ctx, dir, opts...)` returns a `*SafetyScissors`. It lays out
  `jobs/`, `sessions/` and `activity/` under `dir`; `WithDirs` moves any of them, and
  refuses `Jobs` and `Sessions` in one directory. `Calls` reads the activity log through
  `journal.Calls` ([ADR 0051](0051-the-journal-is-one-package-shaped-like-log-slog.md)).
- `WithInvocation`, `WithActor` and `WithNotices` are required, and `New` names every
  missing one at once as an `*OptionError`. `WithNotices` joins the two the proposal
  named because a job store without it refuses every write, and the module never prints.
- `WithSlots`, `WithMachine`, `WithTiers` and `WithSandbox` are optional. A part left
  out is absent: its accessor (`Slots`, `Machine`, `Tiers`, `Sandbox`) and the calls that
  need it (`Command`, `SpawnCeiling`) return an `*UnconfiguredError` naming the option.
  A budget below 1 is refused rather than read as unlimited. Under `WithSandbox`, `New`
  probes the host once, so a Required policy on a host that cannot confine fails there.
- `Command(ctx, lease, name, args...)` is an unstarted child confined to the binds and
  narrowed to the lease's write paths. It refuses a lease the store does not hold and one
  whose row is not live, since an ended lease narrows nothing.
- `SpawnCeiling(ctx, child)` applies `tier.Ceiling` to the child row and its stored
  parent. Choosing a tier stays the caller's, as part 7 says.
- `WithJobOptions` and `WithClock` carry the job store's other seams and one clock.
  The job store's verbs stay on `Jobs()`, unmirrored, so call sites still read
  `job.Covers` and `verdict.Deny`. Every subpackage stays importable on its own.
- No `Judge` and no `Close`. A judge with no rule table would answer the default allow
  part 7 refuses; it comes with step 10. Nothing here holds a descriptor to close.

Principles checked: [No silent defaults](../design/judgment/no-silent-defaults.md) is why
an absent part answers with the option to pass and never with `nil, nil` or an unlimited
budget. [Delete before you add](../design/principles/delete-before-you-add.md) is why the
store's verbs are not mirrored on the root type.

### 2026-11-17: job stores migrate to the module's groups

- Every job store an earlier magus wrote is `batch.magus/v1alpha1`, so refusing that group
  would leave every store unreadable after the change. A stored-format migration in
  `internal/job` (`groupsMoved`) rewrites the rows, the header and attempts.jsonl to
  `safetyscissors.job/v1alpha1` once, under the store's lock, annotations keyed under the
  old group included. It is the repository's migration convention, not a shim: no reader
  keeps the old group, and a line already moved is left as it is.
- A line in any other group, or a version no migration reaches, is refused as MGS3058,
  which names the line and says to move the store aside and fork the job again, or start a
  fresh session. A newer version of the job group is MGS3047. This replaces the earlier
  refusal of the magus group, which named no job-store code. Ruled by Eli on 11-17.

### 2026-11-17: the job module's surface is trimmed

- The `internal/job` compat forwarders are gone; callers name the module's `Actor`,
  `RefusedError`, `ReadBoundary`, `MaxJobEntries`, `Narrowed` and `OpenEntry`.
- `job.APIVersion` is the one typed constant (`apiversion.APIVersion`) for the job shape
  and every line of jobs.jsonl and attempts.jsonl; `RecordAPIVersion` is gone, and
  magus converts it to `types.APIVersion` where it compares.
- What builds only on the exported store moved to `internal/job`: `EntryAdvice`,
  `Entries`, `ParsePauseUntil`, `ReleasedUnderPattern`, `OwedNoSeat`, `OwedRemoteCI`, the
  stale-store fix and the span digest. What the module itself reads stays in it.
- One renderer draws a check, `internal/job.checkArgv`, led by the binary as invoked;
  `CheckCommand` joins it.
- The three logs beside the rows report a dropped unfinished line as a store notice.
- LeaseCheck stays typed in the module until the binding generators learn a raw-JSON
  member, an open question for Eli. Option A teaches the generators to carry
  `codec.RawMessage` as `any?`, which costs Buzz callers the typed `LeaseCheck` object.
  Option B has the bindings reflect magus-side view types of `Goal` and `Job` that carry
  the typed check, which reshapes the boundary roots in `cmd/magus-utils`.

### 2026-11-17: steps 9 and 11 and goal grading are built

- 2026-11-17, step 9: the job row store lives in `libs/safetyscissors/job`, opened on
  the directory the caller names. It holds the row store and its write pipeline, and
  magus keeps grading from its output store, validation, the sweep and checkouts in an
  adapter that embeds it, as the 2026-11-16 decisions say. The package table's `job` row
  now describes what exists.
- 2026-11-17, step 11: `libs/safetyscissors/sandbox` holds read, write and exec
  confinement narrowed to a lease, landlock on Linux and `sandbox-exec` on macOS, refused
  on Windows, and required by default. magus's own modes do not confine a child on
  macOS yet.
- 2026-11-17, goal grading: a check goal names the bases it accepts (`accept`), and a
  check goal accepting none is a fault. `Store.Witness` watches a command the caller
  runs, `Store.Attest` records a statement the caller's `Verifier` accepts, and
  `Store.ReportRun` records the caller's word; `Store.Grade` grades each check goal from
  the run its ref names. Runs and grades are lines of `grades.jsonl` beside the rows, not
  the prototype's in-memory hash chain. Stored check goals were rewritten once to accept
  `witnessed` by a migration. The lease's own check accepts `witnessed`, and magus
  resolves an unnamed accept on a declared check goal to `witnessed`. magus does not call
  `Witness` yet: its gates still grade from the output store. Eli to confirm.

### 2026-11-17: what landed against the proposal

The build reached steps 1 to 8 and stopped short of 9, 10 and 11. Where it departs from
the text above:

- Two modules split out beside this one. `libs/secrets` holds step 7, and `libs/codec` is
  the JSON and JSON Lines leaf both magus and this module import. `libs/codec` replaced
  step 4's `internal/jsonl` copy and magus's `internal/json`, so magus keeps no store
  helpers of its own. It builds with or without `GOEXPERIMENT=jsonv2`, which answers part
  2's encoding line differently than written: without the experiment it runs on
  `encoding/json` and refuses a type that build would write differently. The magus
  package refuses to compile without the experiment (`requires_jsonv2.go`).
- `job` is a harness core: the record types and the claim grammar. `Origin` keeps a
  neutral `Credential`, its kind, id and name; the token scheme and its access grant
  are magus's `internal/auth`. `Inflight` stays in `internal/job`. `Worktree.Binary` and
  `BinaryDigest` stay in the core, documented without magus. The package table's `job`
  row still names step 9's store, which has not moved.
- `tier` holds the set and its assignment, and no prices or state. Prices are
  `budget.Price` and `budget.Pricer`, primitives no spend limit consumes yet, shipped
  because Eli asked for them. The price table (`model.Table`, `model.Row`) and
  `model.TierState` are magus's `internal/agent/model`.
- `trail` exports its event and readers only. The guard's record types stayed in
  `types/trail`, since only magus's Buzz API makes them, and the writer moves with the
  guard. The coming fold (ADR 0051) renames the package and its event.
- The module never answers yes: its decisions are defer, advise, ask and deny, and a host
  adapter writes no permission decision for a defer (Cursor, which must answer, gets its
  ask). `defer` replaces both of ADR 0039's names, `allow` and `pass`, and a record
  holding either is refused rather than read.
- The records carry the module's own API groups, which answers the last open question:
  `safetyscissors.job/v1alpha1`, `safetyscissors.job-result/v1alpha1`,
  `safetyscissors.step/v1alpha1`, `safetyscissors.verdict/v1alpha1` and
  `safetyscissors.session/v1alpha1`, declared once in
  `libs/safetyscissors/internal/apigroup`. There is no shim: a record stamped with the
  magus group it carried before is refused, naming the record and saying to fork the job
  again or start a fresh session. Picked by Eli on 2026-11-16.
- No setters. `secrets.SetProviderName` and `Limiter.SetHooks` are gone:
  `Resolver.WithProvider` derives a resolver that shares the cache, and the limiter takes
  `WithPoolEvents` when cmd/magus builds it after telemetry exists.
- Evaluating a magusfile returns its provider and remote-backend choices as data, and the
  registry setters that held them are gone.
- The session fold cache is one JSON document rewritten whole, not JSON Lines. It is the
  one exception to [everything is a file](../design/principles/everything-is-a-file.md)
  in the module: the cache is derived and never the authority, keyed by each invocation
  file's identity, and a torn or undecodable one refolds from the files. An append history
  of folds would hold nothing a reader wants.
- `doc_test.go` allows a `libs/` requirement resolved by a `replace`, and the release
  refuses one that is untagged.
- The briefs that built the steps were numbered apart from the table: ss-0 is step 1,
  ss-1 step 2, ss-2 step 3, ss-3 step 8, ss-4 steps 6 and 7, ss-5 step 5, ss-6 step 9,
  ss-7 step 11 and goal grading, ss-8 and ss-9 step 10, and ss-10 the docs and the API
  lock.

The text above is kept as written, and the State column says what exists.

### 2026-11-16: decisions for steps 9 and 11, for the owner to confirm

The orchestrator took these while Eli was away, for the job store and sandbox briefs.
None is built yet.

1. The module's job store holds only the row store and its write pipeline: the store,
   its versions, the lock, the authorization core, grants, the mutation pipeline, the
   wire record and the pure narrowing predicates. magus keeps grading and validation in
   an adapter `Store` that embeds it, `NewStore(Location)` keeps its signature, and magus
   wires the rest as functional options.
2. The check grader stays in magus. The process-global `SetCheckGrader` is deleted for
   `Location.Grader`.
3. The module sandbox refuses Windows loudly in required mode. magus keeps its current
   Windows best-effort.
4. macOS `sandbox-exec` ships in the module, but magus's existing modes do not start
   confining children on macOS in that change. magus opts in later, with Eli's yes.
5. `internal/store/migrate` stays in magus, since package names are Eli's pick. The
   module store takes its migrations through an option.
6. The sandbox's grant gets a name that collides with neither `job.Grant` nor
   `filesystem.Rule` and never uses the allow vocabulary.
7. `launch_linux.go`'s `//nolint:gosec` is restructured away, through `unix.Exec`, and
   no exclusion replaces it.
8. The grading migration that writes `accept: [witnessed]` onto stored check goals is a
   one-time rewrite by compaction, not a dual reader.
