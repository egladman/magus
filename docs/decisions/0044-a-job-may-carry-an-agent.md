---
title: "ADR 0044: a job may carry an agent, and an attempt is one try of it"
order: 44
description: Two kinds in group batch.magus/v1alpha1. A Job is the unit of work, and its graded part names no model. Agentic work adds one optional spec.agent section (requested model, instructions, decisions, evidence, and whether a new worker or the warm one takes it). In Go that section is agent.Spec in types/agent, beside the model tier types that move there, and types.Job.Agent holds it. An Attempt is one try, and the job result becomes its status. The records are Kubernetes CRD-shaped; each has an unexported persistence type in internal/job beside jobs.jsonl, as ADR 0030 lays a store out. There is no Task kind.
tags: [adr, decision, jobs, agents, records, stores]
status: proposed
date: 2026-11-11
---

# ADR 0044: a job may carry an agent, and an attempt is one try of it

## Context

Measured on main at 5f0df014d, 2026-11-11, with `magus refs` and reads of
`types/job.go` and `internal/job`:

- `types.Job` (2872 lines in `types/job.go`) carries the agent fields `Model`,
  `ModelTier`, `FreshReason` and `Criteria` as flat members beside what magus grades.
  `types.JobPut` carries `Model`, `Criteria` and `FreshReason`. `magus refs Job.Model`
  names 30 files, `Job.FreshReason` 8. `Job.ModelTier` and `Job.Criteria` also reach the
  proto (`proto/gen/go/magus/job/v1alpha1/job.pb.go`) and the job service
  (`internal/handler/job/service.go`).
- `Job.Holder` (`server` or `session`, 17 files) tells the server's maintenance runs
  apart from work a session handed out. Neither kind needs a second record: a server
  run is a Job with no agent.
- `jobs.jsonl` stores `types.Job` directly (`jobsFile.Jobs []types.Job`,
  `internal/job/store.go`). `job.Store` already owns a sibling file: `grants.jsonl`
  through `file.JSONL[types.JobGrant]` (`internal/job/grants.go`).
- `types/` already has domain packages (`types/trail`, `types/daemon`, `types/diff`,
  `types/events`, `types/image`). `types/daemon` imports `types`.
- PR #734 put flat `model` and `criteria` members on the job row, and Eli rejected it on
  2026-11-11. The worker that followed added a middle `Task` kind, `internal/apis/job/v1alpha1`,
  `internal/taskstore` and agent fields in `types/task`. Eli rejected that for ignoring
  the layout above. A redesign on 2026-11-11 first kept `Task` as a kind of its own,
  exactly one per Job. Eli then dropped it: a record that always maps one to one to its
  Job is a join with nothing gained, and the Job is already magus's unit of work.

## Decision

### 1. Two kinds: job and attempt

- A **Job** is a unit of work, agentic or not. Its graded part (paths, the check,
  the success rules, `needs`) names no model, tier or prose.
- An **agent** is one optional section of a Job, `spec.agent`: the requested model
  `{id, tier}`, the instructions, the decisions with their reasons, the evidence, and
  `worker {type, reason}`. `type` is `Resumed` or `New` and is always written. `reason`
  says why a new worker starts instead of the warm one of the job this one
  `follows`. It replaces `fresh_reason` (Eli, 2026-11-11). A Job without the section
  is ordinary work, such as a server maintenance run.
- An **Attempt** is one try of a Job. The job result becomes its `status`. What the
  workspace resolved the model to (`status.agent.model.resolved`) and what the
  harness's own record observed (`status.agent.model.observed`) are per try, so both
  live on the Attempt.

Eli chose these on 2026-11-11.

### 2. The agent section is its own domain package, and the job holds it

On the wire the agent section is the Job's `spec.agent` and the Attempt's
`status.agent`. In Go it is `agent.Spec` and `agent.Status` in the new domain package
`types/agent`. Agent types get their own domain package and never sit in top-level
`types`, so the model tier types (`ModelTier`, `JobModelTier`, `ModelTiers`,
`ModelTierSet`, `ModelTierRequest`) move there too. Then `types` imports
`types/agent`, never the reverse, and `types.Job.Agent` and `types.JobPut.Agent` are
`*agent.Spec`: nil is ordinary work. Any reader, a grading rule included, reads the
field. A rule comparing the requested model with the observed one is deterministic and
is the store's to compute.

### 3. Records are `CRD`-shaped, and the conversion lives in the store

Every kind is `apiVersion`, `kind`, `metadata`, `spec`, `status` in group
`batch.magus/v1alpha1`. That is the `<group>.<domain>` order of a Kubernetes CRD
group, and it departs from the `magus.<format>` spelling of other magus formats. Keys
are camelCase and enum values CamelCase. Lifecycle is `status.conditions[]` with no
`phase`. A chosen identifier is `metadata.name`. These records alone take that spelling;
every other magus record stays snake_case.

The record is the external contract and may differ from the Go domain type. Following
ADR 0030, each kind has an unexported persistence type in `internal/job` (`jobRecord`,
`attemptRecord`). It is converted to and from the domain types in one place per kind,
in that kind's file there. The Go enums keep their lowercase values, and the conversion
maps them. The envelope is an unexported generic in `internal/job` until
`libs/apiversion` provides `TypeMeta`. Then it moves onto that type.

### 4. Layout

| concept | domain type                                   | record                                          | file                                 |
| ------- | --------------------------------------------- | ----------------------------------------------- | ------------------------------------ |
| Job     | `types.Job`, `types/job.go`                    | `jobRecord`, `internal/job/store.go`            | `jobs.jsonl`                         |
| agent   | `agent.Spec`, `agent.Status` and the model tier types, `types/agent`; `types.Job.Agent` | `jobRecord.spec.agent`, `attemptRecord.status.agent` | none of its own |
| Attempt | `types.Attempt`, a new file in `types/`        | `attemptRecord`, a new file in `internal/job`   | `attempts.jsonl` beside `jobs.jsonl` |

`attempts.jsonl` uses `file.JSONL`, as `grants.jsonl` does.

### 5. Build order

1. The model tier types move into `types/agent`, as a mechanical move in a commit of its
   own.
2. The agent section. `Model`, `ModelTier`, `FreshReason` and `Criteria` leave
   `types.Job` and `types.JobPut` for `Agent *agent.Spec`, stored as `agent` on
   `jobs.jsonl` before the envelope changes, with a store migration. After this step
   the graded members of a Job name no model.
3. Attempt. `attempts.jsonl` takes the job result, `Prior` and the attempt counters.
4. The CRD envelope and camelCase keys on both files. Conditions replace `state`, and
   `Holder` goes: a Job with no agent section and the server as its holder is the
   server's run.

### State

| item                                                                                                  | State    |
| ----------------------------------------------------------------------------------------------------- | -------- |
| model tier types moved into `types/agent`                                                             | done     |
| `agent.Spec` as `types.Job.Agent`, `JobPut.Agent`; the four flat fields deleted; store migration      | done     |
| `types.Attempt`, `attempts.jsonl` with `attemptRecord`; `Prior` and the job result move there         | proposed |
| CRD envelope in `batch.magus/v1alpha1` on both files; conditions replace `state`; `Holder` deleted    | proposed |
| envelope on `libs/apiversion` `TypeMeta`                                                              | proposed |

### Deliberate break

Pre-1.0, with no compatibility reader
([Break now, hold still later](../design/principles/break-now-hold-still-later.md)). A
`jobs.jsonl` row's `model`, `model_tier` and `criteria` move under `agent` when the
store migrates it, and `fresh_reason` becomes `agent.worker` with `type: New`. The JSON output of `magus job` and `describe job`
shows them under `agent`. The job proto's `model_tier` and `criteria` fields move into
an agent message.

### Principles checked

- [A harness is not an agent thing](../design/principles/a-harness-is-not-an-agent-thing.md):
  relies on it. A Job is work, and the agent is one optional section.
- [Delete before you add](../design/principles/delete-before-you-add.md): the middle
  kind was deleted before it was built.
- [Everything is a file](../design/principles/everything-is-a-file.md): one append-only
  JSON Lines file per kind, with encoding on the persistence type.
- [Abstract at the right layer](../design/principles/abstract-at-the-right-layer.md):
  the job repository owns both files. There is no generic record store and no second
  tree.
- [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  each step migrates the store and moves every reader in the same change.

## Alternatives

### A task kind, one per job

`kind: Task` in `tasks.jsonl`, named as its Job, holding the work and its agent
section. It lost because it always maps one to one to its Job, so every reader pays a
join and nothing is gained; magus's domain type for a unit of work is already the Job.
Evidence: Eli, 2026-11-11, after reviewing full JSON examples of both shapes. It
reopens if one Job ever needs several units of work under one grade.

### An agent kind of its own

`kind: Agent`, keyed by the job name, with its own generation. It lost because the
agent has no lifecycle apart from its Job, and a second generation would let an Attempt
pin a Job and an agent that disagree. It reopens if one agent configuration must be
shared across Jobs.

### Keep the agent apart from `types.Job` in Go

Leave the model tier types in `types`, so `types/agent` imports `types`. Then
`types.Job` and `types.JobPut` cannot hold the agent, and every door (the CLI, Buzz
`putJob`, the proto) passes the agent as a second value beside the Job. It lost because
it doubles every door to dodge a cycle that a move of agentic types into their own
package removes. The earlier claim that it kept grading from reading the model was
wrong: a deterministic rule over the agent section is magus's to compute. Evidence:
`magus refs` on 2026-11-11 counts 21 to 29 files per tier type, mostly the same files.
Eli chose the move on 2026-11-11. It does not reopen.

### A parallel tree: `internal/apis/job/v1alpha1`, `internal/taskstore`, `types/task`

It lost because `job.Store` already owns sibling files (`grants.jsonl`) and ADR 0030
already fixes where a domain type and a persistence type live. Evidence: Eli's
rejection of 2026-11-11. It does not reopen.

### Flat agent members on the job, as in #734

It lost because a graded record with flat `model` and `criteria` members makes every
job agent specific. Evidence: Eli's rejection of 2026-11-11. It does not reopen.

## Consequences

- A reader that wants the model reads `Job.Agent`. In step 2 that covers the spawn
  rules, the warm-worker rule, describe job, the job service and proto, and
  `hack/policy/spawn.buzz`.
- An Attempt per try removes the cap of three on `prior`.
- A server run needs no record beyond its Job.

## Open questions

- The name of the prose list: `instructions[]` (proposed), replacing `criteria`. An
  earlier draft had `readme[]`.
- `maxAttempts` keeps today's meaning. Kubernetes' `backoffLimit` counts retries, one
  fewer than attempts, and implies a backoff magus never runs.
- `libs/apiversion`'s `TypeMeta` keys: these kinds need camelCase, and
  `types.Schema` uses `api_version`.

## Amendments

None yet.
