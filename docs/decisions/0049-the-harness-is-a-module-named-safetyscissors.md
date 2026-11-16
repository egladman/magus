---
title: "ADR 0049: the harness is a module named safetyscissors"
order: 49
description: "magus's harness layer becomes its own Go module, safetyscissors. The layer is guard verdicts, job leases and goal grading, the trail, sessions, the sandbox policy, the build budget and the model tier table. A Go program imports it with no magusfile, no magus.yaml, no magus binary, no workspace and no version control. It stays in the magus repository as a nested module, and magus becomes its first client. The name says what the library is for: the tool still works, and you cannot hurt yourself with it, whoever is holding it. It ships no agent loop, no model client and no router. This record is written before any code; nothing is extracted until the open pull requests on magus merge."
tags: [adr, decision, sdk, harness, guard, jobs, module]
status: proposed
date: 2026-11-15
---

# ADR 0049: the harness is a module named safetyscissors

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
  the broker and VCS. `Judge` (`internal/guard/guard.go:766`) runs the 106 built-in rules
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
  (`internal/cache/limiter.go`) imports nothing from magus.

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
diagnostics already are. Its packages, each with one job:

| Package   | Holds                                                                                       |
| --------- | ------------------------------------------------------------------------------------------- |
| `verdict` | `Decision`, `Verdict`, `Judgment`, the stricter-of merge, `DeniedError`                     |
| `step`    | the step and its declared rows (ADR 0046), `Rule`, `Table`, `(Table).Judge`                 |
| `job`     | the job store opened on a directory, `Job`, write paths, goals, fork, exit, grade, `Covers` |
| `trail`   | append-only events, written and read                                                        |
| `session` | session ids and their append-only records                                                   |
| `sandbox` | read, write and exec policy, narrowed to a lease                                            |
| `budget`  | the slot limiter and machine claims                                                         |
| `tier`    | the model tier set and price table built from rows the caller supplies                      |
| `redact`  | resolving secrets and redacting them from what is recorded                                  |

The root package holds only the package doc. Call sites read `verdict.Deny` or
`job.Covers`, never a themed name.

### 2. It runs with nothing but a directory

- A caller opens every store on a directory it names. Nothing resolves a repository
  identity, a user state directory or a working directory on its own.
- Nothing reads the environment or writes to stdout or stderr. Each of those is an option
  the caller passes.
- Nothing is process-global. The grader `SetCheckGrader` sets today becomes an option on
  the store.
- The core imports only the standard library and the repository's own `libs/*` modules.
  OpenTelemetry, if it comes, is a separate sub-module.
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

The module ships no built-in rule that reads magus's world: worktrees, `magus run`
routing, approved sources and the magusfile all stay in magus. Rules that read only the
step and its lease, such as a write outside the lease's paths, move with the module.

### 4. magus is the first client

`judge.go` builds a `step.Table` and registers magus's built-ins and Buzz adapters as
rules. `internal/job` keeps worktrees, checkouts, landings, merges, plans and outcomes on
top of the module's job store. `vcs.StateDir` computes the directory magus passes to
`job.Open`. The CLI, the hooks, `magus agent install`, the MCP server and the
workspace's rules stay in magus. The module never imports magus.

### 5. The seams

Only seams that already have a second implementation become interfaces:

- `job.Reader`: the module's store, and an embedder's own rows (`types/job_reader.go:8`).
- `job.Observer`: magus's VCS-and-output reader, and a caller's own check result
  (`internal/job/verify.go:36`).
- `redact.Provider`: the environment, and the broker opener
  (`internal/secret/secret.go:46`, `:104`).

Repository identity becomes a directory string, not an interface. Each seam gets one
option and a fake in `libs/testkit`.

### 6. What it refuses

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
- anything that rewrites what reaches the model.

The names carry no agent vocabulary, because the mistakes it prevents are the ones a
person makes too.

### 7. The name is `safetyscissors`

Safety scissors cut. You cannot stab yourself with them, whoever holds them. That is the
library's purpose stated as an object, and it says nothing about agents.

- On 2026-11-15, neither pkg.go.dev nor GitHub had a module or repository named
  `safetyscissors`.
- The theme is deliberately its own, apart from magus's magic vocabulary, so the name
  does not read as a magus internal.

Eli picked it on 2026-11-15.

### 8. Extraction order

Each step merges on its own and leaves magus green. No step starts until the open
pull requests on magus have merged.

| Step | Moves                                                                                                | Size                          | Needs Eli's call                                          | State   |
| ---- | ---------------------------------------------------------------------------------------------------- | ----------------------------- | --------------------------------------------------------- | ------- |
| 1    | `budget`: `internal/cache/limiter.go`, the machine claim and its three types                         | ~500 lines                    | no                                                        | planned |
| 2    | `verdict` and `step` types out of `types/guard.go`, `types/guard_judgment.go`, `types/agent/step.go` | ~700 lines                    | yes: breaks the Go API lock                               | planned |
| 3    | `tier`: `types/agent/model.go`, `internal/agent/model`, `TierCeiling` (`register.go:111-117`)        | ~650 lines                    | no                                                        | planned |
| 4    | the JSON Lines store helpers the module's stores need                                                | ~1,500 lines                  | no                                                        | planned |
| 5    | `trail` core; feedback marks, replay and env propagation stay in magus                               | ~1,500 lines                  | no                                                        | planned |
| 6    | `session`, with the directory supplied by the caller                                                 | ~2,000 lines                  | no                                                        | planned |
| 7    | `redact` from `internal/secret`                                                                      | ~1,300 lines                  | no                                                        | planned |
| 8    | the job types out of `types`, with no alias, and the generated Buzz mirrors regenerated              | ~5,000 lines                  | yes: 16+ importers                                        | planned |
| 9    | the `job` store core, with config as `Limits`, VCS as a directory, env and stderr as options         | ~8,000-10,000 of 23,431 lines | yes: the `jobs.jsonl` location and the record's API group | planned |
| 10   | the guard engine: the step table and settings, with magus's 106 built-ins registered as rules        | the largest                   | yes: `Judge`'s structure changes                          | planned |
| 11   | `sandbox`, after step 9, with its own grant type replacing `spells.Sandbox`                          | ~2,800 lines                  | no                                                        | planned |

Sizes are estimates from the line counts above, not measured diffs.

### Principles checked

- [A harness is not an agent thing](../design/principles/a-harness-is-not-an-agent-thing.md):
  the module is the same checks for whoever holds the tool. The name and the package
  names say so by mentioning no agent.
- [The harness sits on the tool layer](../design/principles/the-harness-sits-on-the-tool-layer.md)
  and [the outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  the module judges, leases, records and grades. It never runs, edits or routes the
  caller's loop.
- [What we do not build is the product](../design/principles/what-we-do-not-build.md):
  part 6's refusals are the reason the module exists beside the agent frameworks, not a
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

### An agent framework, like Mastra

- Why it lost: it is refused, and it would compete where the field is crowded while
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
- The job store's directory comes from the caller, so magus decides whether rows stay
  under `$XDG_STATE_HOME/magus/jobs`, a decision step 9 makes.
- The module is tagged as `<module path>/vX.Y.Z`, as the other nested modules are, and
  gets its own Go API lock.
- [The Go SDK reference](../reference/go-sdk.md) changes its "the harness is CLI-only"
  line once step 10 merges.
- `internal/agent/harness` keeps its name. It is magus's host test driver and never
  enters the module.

## Open questions

- The module path. Two options:
  - `github.com/egladman/magus/libs/safetyscissors` matches the existing nested modules,
    but changes every importer's path if the module ever leaves the repository.
  - A vanity path survives a move. It needs a domain and a `go-import` tag that points
    at a subdirectory. Which Go release supports that has not been verified.
- Which of magus's 106 built-in rules read only the step and its lease, and so move with
  the module. Part 3 names one; the rest are unsorted.
- The record's API group: job rows carry `batch.magus/v1alpha1` today (`internal/job/store.go:338`).
- Whether a caller can append to the trail directly, or only through judging and job
  events.
- What an `ask` verdict does for a caller that can prompt a person. Today an ask is
  denied (`judge.go:23-24`).
- A spend budget for a caller's loop. The budget covers memory and slots; price is
  computed only when a job ends.
- Whether goals can be graded from a caller's own check result with no recorded magus
  run (`types/job.go:155`). This has not been verified.
- Sandbox confinement off Linux. Only `landlock_linux.go` exists.
- A read-only door to graph answers, the part of magus the survey found nobody else
  has, without pulling the graph into the module.
