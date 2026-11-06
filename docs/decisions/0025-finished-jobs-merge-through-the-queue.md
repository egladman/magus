---
title: "ADR 0025: finished jobs merge through the queue"
order: 25
description: >-
  Whether finished magus jobs should land on a local branch through the merge queue magus already has, instead of the root forking an integration row and spawning an integrator per wave. This record proposes a `jobs` provider for `magus queue`. Merge intent is a job row reaching `pass`, the queue's base is a local branch that is never `main` and never pushed by the queue, and verification runs each merged job's own checks against the machine's one build budget. A person still decides what is pushed and what merges. Nothing is built.
tags: [adr, decision, queue, merge-queue, jobs, agents, orchestration]
status: proposed
date: 2026-11-05
---

# ADR 0025: finished jobs merge through the queue

## Context

A job is not done until its branch is on the branch the root lands from. Today that takes
one integration round per wave, by hand
([The integrator merges, regenerates and gates](../guides/integrations/agents/orchestration.md#the-integrator-merges-regenerates-and-gates)):

1. The root forks a row whose `integrates` names the finished branches.
2. It spawns `<parent>/integrate <job>` in its own checkout.
3. The integrator runs `hack/dev/merge-job-branches.buzz -- --jobs <ids> --into <branch> --apply --verify`.
   The script orders the branches by `dependsOn`, merges each with `git merge --no-ff`, settles
   files split by job claims, regenerates once, recomposes the landing by job, probes the policy
   when the merge changed the load contract, and runs every merged job's checks.
4. The root runs `magus job wait` on the integration row and merges the result into its branch.

What exists to do the same work without the round:

- `magus queue` is a speculative, partitioned merge queue
  ([Merge queue](../concepts/merge-queue.md)). `plan` partitions changes by affected set,
  `validate` builds one candidate per change (the base plus every change ahead of it in its
  partition) and runs hooks on up to `--depth` of them at once, and `apply` rebuilds each green
  candidate, runs none of the change's code, and merges it only when the rebuild is the commit
  validation checked. It already settles drift in the candidate: a conflict the low-risk
  classifier resolves, and generated files the merge leaves stale.
- A provider is a Buzz script exporting thirteen functions
  ([Providers](../concepts/merge-queue.md#providers)). `--provider github` is built in;
  `--provider path/to/provider.buzz` loads any other. The provider's host module
  (`internal/queue/provider/host.go`) binds `request` (HTTP), `codeSpan` and `codeBlock` beside
  gopherbuzz's standard library. It has no version-control or job-store call.
- `apply <dir>` reads the plan and verdicts from the directory `validate --verdicts` writes, so a
  local run needs no `list_artifacts` and no workflow run.
- The job store records each row's state. `magus job exit` records `exited`; only the forker's
  `magus job wait` records `pass`, after reopening the run behind the evidence and checking the
  diff against the write paths ([Jobs](../guides/jobs.md)). `magus ls jobs` already joins the
  queue's newest snapshot as its `in flight` line.
- [ADR 0024](0024-magus-queue-is-a-plugin-binary.md) (proposed, merged as #644) moves the queue
  into a `magus-queue` binary that links `internal/job` among other packages of the module.

The forces, from this tree on 2026-11-05 at `be17ecc08`:

- **The round is a person-visible cost with no measurement.** Eli noted on 2026-10-31 that the
  root merging stacks by hand is a large share of wasted time. Nothing records it: a row
  carries `created`, `registered` and `updated`, and no field says when its branch landed.
- **Landing day showed the failure modes of hand merging.** `main` took 22 squash merges dated
  2026-11-05 (`git log origin/main --since=2026-11-05`), seven of them between 22:56 and 23:05.
  The harness gaps recorded that day
  (kept outside the repository, in the maintainer's session plans as `harness-gaps-2026-11-05.md`) include:
  - gap 1: merging `main` into #643, which changed the load contract, deadlocked the guard.
    The stale-binary check refused `magus vcs resolve` and `git commit` on the uncommitted merge,
    the checkout bootstrap refused the dirty tree, and Eli ran `cp` and `git commit` by hand.
    #651 lets the merge of the build commit through.
  - gap 4: the finished-branch fork limit (`finishedBranchLimit = 10` in
    `hack/policy/spawn.buzz`) counted six branches already squash-merged to `main`, so every fork
    was refused. #651 skips landed branches.
  - gap 8: a test under `magus run test libs/editor` started 2,311 `app.test` processes and hung
    WindowServer. #654 caps a command's process tree and reaps `setsid` descendants. Any local
    verification runs the jobs' code, so it inherits this risk and this fix.
- **Partitions buy little on this tree.** The queue's affected sets are projects
  (`client.Workspace.Affected`). `magus describe file internal/queue/partition.go` puts it in
  project `.`, which owns `internal/`, `cmd/` and `hack/` alike. Of the 19 projects
  `magus ls projects` lists, a job touching any file `.` owns reaches `.`, so nearly every job
  of a wave falls into one partition. Separate partitions arise for jobs
  confined to `libs/*`, `console` or `docs`.
- **One machine, one budget.** Every magus run on the machine draws on the broker's budget
  ([Concurrency](../concepts/concurrency.md#across-the-whole-machine-the-budget)). A claim that
  cannot fit is refused at once with MGS3009 unless its caller sets `budget_wait`, whose default
  is 0 (#641). Workers' checks and any local verification compete for the same seats.

## Decision

Every row below is _proposed_.

### A `jobs` provider

`magus queue --provider jobs` reads the job store and the local repository. It ships beside
`github.buzz` in `internal/queue/provider`, because the job store is magus's own, not this
repository's: any magus workspace that forks jobs has one.

| Provider function                                                | What `jobs` answers                                                                                                               | State    |
| ---------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------- | -------- |
| `describe`                                                       | `stackMerge: sequential`, `methods` per the decision below, `requiredApprovals: 0`, no `setup`, the committer named below         | proposed |
| `list_changes`                                                   | each row in state `pass` with commits its branch holds and the base does not, in the order the rows reached `pass`                | proposed |
| `merged`                                                         | rows whose branch head the base already carries                                                                                   | proposed |
| `unqueued`                                                       | rows that exited and are not yet `pass`: a queued change carrying one's head waits with `WAIT_UNQUEUED_BELOW`                     | proposed |
| `parent`                                                         | the row's `dependsOn` entry whose branch the head descends from, a hint `plan` checks against ancestry as it does today           | proposed |
| `approval_at`                                                    | `approved` when the row's `pass` was recorded at that head; `queued` while the row is `pass` and carries no `left_unmerged`       | proposed |
| `post_status`, `list_green`, `mark`                              | lines in one JSON Lines store per repository beside `jobs.jsonl`, last line per change and context wins                           | proposed |
| `merge_change`                                                   | a compare-and-swap update of the base ref from the tip apply read to the commit it rebuilt; a moved tip is a refusal, not a retry | proposed |
| `kick_back`                                                      | a line in the same store naming the code, files and reproduce lines; `magus ls jobs` shows it on the row                          | proposed |
| `list_artifacts`, `required_checks`, `reviews`, `dismiss_review` | not exported: a local run reads a directory source and has no reviews                                                             | proposed |

Merge intent is a row reaching `pass`. A worker's `job exit` is the worker's word and queues
nothing. The forker's `job wait` is the verification that a person or the root already runs,
so the queue adds no second approval.

### The base is local and is never `main`

The queue's base is a local branch the root names, the one it would push for a PR. The `jobs`
provider refuses a base that is the remote's default branch, and refuses a base any worktree
has checked out, naming the worktree: an update must never change files under someone. The
queue never pushes. `validate` and `apply` work in a full clone of the repository in
`$XDG_CACHE_HOME/magus/tmp`, whose remote is the repository's own path, as they already require
a full clone and a named remote.

### Verification runs the jobs' own checks, inside the budget

Each job already passed its check in its own worktree. What a candidate adds is the merge, so
`validate --gate` runs the checks of the jobs the candidate merged, as
`merge-job-branches --verify` does, not `magus affected ci`. The full verification stays where
it is today: once on the final tree, and in PR CI.

- Each hook is a `magus` run that claims seats like any other and passes `--budget-wait`, so a
  full machine parks the candidate instead of refusing it.
- `--depth` defaults to 1 for `jobs`: one candidate at a time, each over every change ahead of
  it, so a batch of N passing jobs costs one verification of their merge. A deeper run is the
  person's flag.
- A candidate whose diff touches the load contract (the paths `hack/magusfile/load-contract`
  lists) builds its own `./magus` before its hook runs, as the checkout bootstrap does.
- A red candidate is bisected the way the queue already does it: the change whose own delta
  fails is kicked back, and the changes above it rebuild without it.

### What a person still decides

- Which work exists, through the rows they or the root fork, with criteria and checks.
- That a row passed: `job wait` is run by the forker, and the queue reads its verdict.
- What is pushed: the queue moves a local branch only. The root pushes it and opens the PR.
- What merges: Eli merges each PR. The queue never touches `main`, auto-merge or a provider's
  queue.
- To hold a row back: `job apply` with `left_unmerged` removes intent, which `approval_at`
  reports as `queued: false`.

### With the queue plugin

ADR 0024 does not change this record. `--provider jobs` is one more built-in provider of
`magus-queue`, which already links `internal/job`. If 0024 is rejected, the provider lives in
the same package inside `magus`. `magus ls jobs` keeps reading the snapshot either way.

### Principles checked

- [Human in the loop](../design/principles/human-in-the-loop.md): relied on. The named points
  (fork, `job wait`, push, PR merge) stay a person's or the root's, and the queue acts only
  between `pass` and an unpushed branch.
- [Manual on purpose](../design/judgment/manual-on-purpose.md): relied on. Merging a verified row
  onto an unpushed branch is a repetition. Choosing what lands is a rep, and it stays at the PR.
- [Agents propose, humans dispose](../design/judgment/agents-propose-humans-dispose.md): relied
  on. A worker's exit queues nothing.
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md): strained. The
  failures removed are the per-wave round and the hand merges of gaps 1 and 4. The round's cost
  is unmeasured, so the burden of proof is not yet met (Open questions).
- [Delete before you add](../design/principles/delete-before-you-add.md): strained. The provider
  adds about thirteen Buzz functions and one host call. It earns its place only if the
  integration row's merge duty, and with it `merge-job-branches --apply`, is deleted once it
  works. Whether to delete them is Eli's call.
- [Optimize the loop that verifies](../design/principles/optimize-the-loop-that-verifies.md):
  relied on. Batching makes a wave's merge one verification instead of one per branch.
- [Provider I/O is Buzz](../design/judgment/provider-io-is-buzz.md): relied on for the provider,
  strained by the host call it needs. The compare-and-swap ref update and the job-store read are
  Go, bound into the provider's host module narrowly.
- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. A `main` base, a
  checked-out base, a moved tip and a missing host call are refused by name.
- [Everything is a file](../design/principles/everything-is-a-file.md): relied on for the status
  store. Strained by the existing snapshot, one rewritten JSON document per base, as 0024 notes.
- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md): relied
  on. The queue is a `magus` command the root runs. It changes nothing the host's loop sends.
- [The orchestrator plans](../design/principles/the-orchestrator-plans.md): changed. The
  integrator no longer merges finished branches. It keeps building `./magus` and the final full
  check. The page changes in the same commit as the build.
- [Built for the largest monorepos](../design/principles/built-for-the-largest-monorepos.md):
  relied on. A workspace with finer projects gets real partitions with no change here.

## Alternatives

### Keep manual integration rows

It loses on the round itself: one row, one spawn and one `job wait` per wave, and a person in
the loop for each hand merge that the guard refuses (gap 1). Evidence: the four steps under
Context, gaps 1 and 4. It reopens if the round, measured from the job store, turns out small
against the waves' own run time.

### A serial auto-merge of passing rows

A repository script, run when a row reaches `pass`, calls
`merge-job-branches -- --jobs <id> --into <branch> --apply --verify` for that one row. It is the
smallest change: no provider, no host call, and the script already settles claims, regenerates
and recomposes. It loses on two counts. One row per run means one verification per job; a batch
of several rows means a red names the jobs whose checks failed and not the change that broke
them, so it is run again by hand. And a mechanism every magus workspace with jobs needs would
live in this repository's `hack/`, though nothing in it is specific to this repository. Evidence:
`verifyLayers` in `hack/dev/merge-job-branches.buzz` grades the merged tree, not a stack of
candidates. It reopens if waves stay small enough that one row per run is cheaper than the
provider, which is Eli's first decision below.

### Run the GitHub queue on pushed job branches

Push each passing job branch, open a draft PR, and let the existing provider queue it. It loses
because it publishes every job branch and puts each through PR CI, the opposite of landing
locally and verifying once. It also needs merge intent on GitHub, which only Eli gives. It
reopens if jobs run on more than one machine, where the local job store does not travel.

### Let the provider run `git` through `os.execute`

The standard library a provider sees already has `os.execute`, so the `jobs` provider could run
`git update-ref` and `magus ls jobs -o json` with no host change. It loses because the concept
page puts processes and version control in Go and keeps a provider a function of a system's
answers, and an `os.execute` provider runs whatever is on `PATH` with the queue's rights. It
reopens if no narrow host call can be agreed.

### Rebase each branch instead of merging

A `rebase` method gives a linear landing branch. It loses as the default because a rebase
rewrites the commits `job wait` verified, so `approval_at` would have to carry approval across a
rebase, which the queue does only for one that changed nothing. It stays available as a method.

## Consequences

- A wave lands without an integration row: rows reach `pass`, the root runs
  `magus queue apply --provider jobs`, and the landing branch moves.
- The integrator role shrinks to building `./magus` and the final full check.
- The finished-branch count must read the queue's base, not `main`, or a landing branch holding
  every passed row still refuses forks (see Open questions).
- A kicked-back job has usually ended and its checkout is removed once clean, so a kick-back
  means a new fork, not a fix in place.
- Validation runs the jobs' code in the queue's candidate box, so a runaway test is bounded by
  the process cap from #654, not by the queue.

### Failure modes

| Failure                                            | What happens                                                                                      |
| -------------------------------------------------- | ------------------------------------------------------------------------------------------------- |
| a job id holds `/` (`refactor/pricing`)            | the queue's change id allows letters, digits, `.`, `_` and `-` only, so the provider must map ids |
| a kept worktree has the job's branch checked out   | the provider refuses to push an update commit there; the change waits, naming the worktree        |
| the root commits to the base while apply runs      | apply's tip check sees the base moved and waits with `WAIT_REVALIDATE`, as it does on GitHub      |
| a candidate changes the load contract              | its hook builds its own `./magus`; without that, the hook fails to load the tree (MGS1021)        |
| the machine is full                                | hooks park on `--budget-wait`; at the bound MGS3009 names the holders and the change stays queued |
| regeneration the proof cannot clear and no bundle  | `KICK_REGENERATION` on an ended job: the root forks a new row                                     |
| two rows' branches conflict outside the classifier | the later row is kicked back with the conflicting files and the commits that touched them         |

## Open questions

- **Is the round worth a provider?** Its cost is unmeasured. Recording when a row's branch
  lands, then comparing `pass` to landed across a week of waves, answers it before anything is
  built.
- **Depth on one machine.** Depth 1 batches; depth 2 or more overlaps verifications that compete
  for the same seats. Whether any depth above 1 pays on a single machine is unmeasured.
- **Method.** `merge` keeps each job's commits as `job wait` verified them; `squash` gives one
  commit per job, which `recompose` produces today. Which is the default for `jobs`?
- **Committer.** The person's `user.name` and `user.email`, or a fixed magus identity for update
  commits?
- **Host calls.** Which two calls the provider host gains: a compare-and-swap ref update and a
  job-store read, or the store's rows passed in the request.
- **Kick-back recipient.** A line `magus ls jobs` shows, or a `magus session notify` to the root.
- **Job ids.** Map `/` to a character the queue accepts, or widen the queue's id rule.
- **The fork limit.** Whether `finishedBranchLimit` counts against the queue's base when one is
  running.
- **Deleting the old path.** Whether `merge-job-branches --apply` and the `integrates` merge
  permission are removed once the provider works.
