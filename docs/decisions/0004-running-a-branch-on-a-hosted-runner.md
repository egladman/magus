---
title: "ADR 0004: running a branch on a hosted runner"
order: 4
description: Whether a laptop can hand one magus invocation on a pushed commit to a GitHub-hosted runner, with no pull request, and read the answer back. Decides a dispatch-only workflow that runs the argv in the merge queue's box and a repository script that dispatches, answers once and forgets; states what "no record" can and cannot mean on a public repository; places it beside ADR 0003's --platform.
tags: [adr, decision, ci, github-actions, remote, sandbox, queue, scope]
---

# ADR 0004: running a branch on a hosted runner

- **Status:** Proposed
- **Date:** 2026-09-26
- **Sibling of:** [ADR 0003](0003-running-an-invocation-on-another-operating-system.md).
  0003 brings another kernel to your checkout; this page takes your commit to CI's
  machine. They answer different questions and compose.

## Context

Two needs arrive at a laptop running many agents. The first is proof: a change that CI
might fail for reasons your machine cannot show, such as CI's kernel build, its mise
toolchain, its uid and HOME, its CPU count, or linux/amd64. The second is offload: a test
suite you would rather not run beside ten agents.

Today the only route to CI's machine is a pull request. A branch push triggers nothing
(`ci.yaml` runs on `push` to main only). `ci.yaml` does carry `workflow_dispatch`, but a
branch dispatch of it runs cold: its local-tier restore and save are `pull_request` only,
and `MAGUS_VCS_BASE_REF=last-passed` has no run log on a new branch to read.

Everything else already exists. `magus queue gate --sandbox=required` is the box the merge
queue gates a candidate in, and `ci.yaml`'s shards run inside it. `setup-magus` builds
magus from the checked-out tree. `.github/actions/magus` passes an argv through its
environment, so a word-split argv never reaches a shell as code. GitHub's dispatch API
returns the run it created (changelog 2026-02-19), and `gh workflow run` prints its URL
since v2.87.0. `hack/ci/merge-queue.buzz` already dispatches `queue.yaml` with `gh`.

## Options

### A. Dispatch `ci.yaml` from a branch

Rejected as the first version. It runs the full plan cold on every call, its concurrency
group and history were written for main and pull requests, and a person wants one argv,
not the whole gate.

### B. `--platform github`, a value of ADR 0003's flag

Rejected. The relay mounts your checkout and returns a store; a runner can do neither. A
run takes a pushed commit, answers in minutes, and returns a log. Folding it into
`--platform` would promise a contract it cannot keep.

### C. A private mirror

Push to `egladman/magus-scratch` and dispatch there, so no run is public. Rejected: it
spends minutes a public repository gets free (2,000 per month on the Free plan, then
$0.006 per Linux minute), duplicates secrets and variables, and hands every agent a second
remote.

### D. One workflow and one repository script (decided)

```sh
magus buzz hack/gha-run.buzz -- dispatch --ref <branch> -- run test .
magus buzz hack/gha-run.buzz -- dispatch --push -- run go::go-test . -- -run TestPipePeer
magus buzz hack/gha-run.buzz -- result --run <id>
magus buzz hack/gha-run.buzz -- forget --run <id>
```

## Decision

1. **`.github/workflows/run.yaml`**, triggered by `workflow_dispatch` alone, with inputs
   `argv` (the magus argv after `magus`) and `sandbox` (`required` by default, or
   `best-effort`). One job on `ubuntu-latest` with `contents: read` and no secret beyond
   the job's own token. It checks out the dispatched ref with no persisted credential,
   builds magus from that tree, prints the revision with `magus vcs checkpoint -o name`,
   and runs `queue gate --sandbox=<mode> --cache .magus -- magus <argv>` through
   `.github/actions/magus`. It reads main's signed remote tier and writes no shared tier.
   It uploads `.magus/logs/` as `magus-logs` on every outcome, kept 7 days.
2. **A repository script, since replaced by `hack/remote/on-actions.buzz`**, three steps, none of
   which waits:
   - `dispatch` needs exactly one of `--ref <branch>` and `--push`. With `--ref` it asks
     GitHub for the branch tip and refuses unless it is HEAD. With `--push` it pushes
     HEAD to `run-<sha12>`, a branch named for the commit. It refuses a dirty tree unless
     you pass `--head`, and then says it tests HEAD without your changes. It refuses an
     argv word the runner would split or glob. It prints the run URL, the `result` and
     `forget` commands, and the host, request count and elapsed time.
   - `result --run <id>` makes one request and says where the run stands, which commit it
     ran against yours, and how to fetch its logs once it completes. It exits 1 once the
     run completed without success, and 0 otherwise.
   - `forget --run <id>` deletes a completed run with its logs and artifacts. It deletes
     the run's branch only when that branch is `run-<sha12>` of the run's own commit, and
     prints what stays visible.

   Every step refuses a run of any workflow but `run.yaml`.
3. **Transport is `gh`.** It holds the laptop's token, and the repository's queue glue
   already shells to it. An HTTP spell comes only if the measurement below says the loop
   earns one.
4. **No waiting.** An agent polls `result` when it has nothing better to do. The guard's
   `ci-watch` deny stays as written.
5. **The workflow must be on main to dispatch at all.** GitHub dispatches only a workflow
   file that exists on the default branch. After that, `--ref <branch>` runs that branch's
   own copy of `run.yaml`, magusfile changes included.

### What "no record" means on a public repository

egladman/magus is public. Anyone logged in to GitHub can open the Actions tab and read a
run's head branch and full logs. GitHub keeps runs, logs and artifacts for 90 days by
default, and a public repository can set 1 to 90. The branch you dispatch on is public the
moment you push it.

So a run CAN mean: no pull request, no review thread, no commit on main, no changelog
entry, and after `forget`, no run in the list and no branch this script pushed. It CANNOT
mean nobody saw it. Between dispatch and `forget`, the run, its logs and its branch are
open to every logged-in user. After `forget`, the commit stays fetchable by its id, and
GitHub does not say a deleted run is gone from its side. Treat a dispatched commit as
published.

### Secrets

Anyone with write access can read every repository secret, and a dispatch at any branch
runs that branch's copy of the file with those secrets in reach. `run.yaml` names no
`secrets.*` beyond the job's token, and `hack/remote/on-actions.buzz` starts `run.yaml` alone.
The guard should prove the rest: deny a dispatch of any workflow whose file at the ref
reads a secret beyond `GITHUB_TOKEN` (`release.yaml` and `release-index.yaml` carry
`workflow_dispatch` and read `MAGUS_SIGNING_KEY`).

### Beside ADR 0003

| | `--platform` (0003) | a dispatched run (this page) |
|---|---|---|
| runs where | this machine, a VM kernel | GitHub's runner, CI's own machine |
| what runs | your working tree, mounted read-only, dirty allowed | HEAD of a pushed branch, clean unless `--head` |
| reproduces | the kernel: landlock, `/proc`, seccomp | 0003's "not reproduced" column: CI's kernel build, the mise toolchain, HOME and uid, CPU count, linux/amd64 |
| results land | the local platform store; `query output` names it | a run log and artifacts; an output ref is a citation, not a replay |
| round trip | seconds once the image is warm | minutes: every run builds magus from source |
| machine budget | yours | GitHub's, free on a public repository with standard runners |
| network | none without `--fetch` | every step, named by the step |

Use `--platform` for the inner loop. Dispatch for the proof, and to move work off a busy
laptop.

## Consequences

- A person or agent gets CI's machine for one argv with no pull request, in the same box
  the queue gates in. It costs a pushed branch, a public run and minutes of wall time.
- Results do not come home as cache entries. The remote tier is CI-only: the
  github-actions spell reads a token the runner gives JavaScript actions alone, and CI's
  entries carry no image digest a laptop could hit on (0003). What comes back is the log,
  the `magus-logs` artifact, and output refs to cite with `describe target --cache
  --against`.
- Runs share the Free plan's 20 concurrent jobs with the merge queue.
- The refusals ledger (`docs/doctrine.md`, "A record of refusals") gains three rows:
  `--platform github`, since a runner can neither mount a checkout nor return a store; a
  default dispatch on push, since a run is a public record you opt into per commit; and a
  private mirror, for the minutes, the duplicated secrets and the second remote.

## Open questions

1. The measurement: dispatch three times from a branch (the landlock tests, a shard's
   argv, a deliberately red argv) and record time to the first log line, the total, and
   whether `result` names a mismatch. The matrix goes on this page with dates and run ids.
2. Who started a run. `dispatch` cannot record the run id on the agent's job row: the job
   store has no field for it. A guard carve-out from `ci-watch` for a run this job started
   needs that field first.
3. The secrets rule above, as a guard rule over `gh workflow run` and `gh api .../dispatches`.
4. A laptop-side `spells/github/workflows` speaking HTTP, beside `review`, if the
   measurement says the loop earns it. No engine verb is proposed.

## Amendment, 2026-09-29

The script is `hack/remote/on-actions.buzz`, a prefix in front of the command you would run
here, like `sudo` or `nice`:

```sh
magus buzz hack/remote/on-actions.buzz -- magus affected ci
```

Its own options come first; the first word that is not one starts the command, which must
be `magus` and passes through untouched. By default it pushes HEAD to `run-<sha12>`,
dispatches `run.yaml`, waits, prints the command's output, exits with its exit code, and
then deletes the run and the branch unless `--keep`. Its first line says where the command
runs, that it spends CI minutes, and the run URL. That replaces decision 4 for the default;
`--detach` prints the run id and returns without waiting, as `dispatch` did. `--ref`,
`--head` and `--sandbox` keep their meaning, and `--push` is the default. `--result <run>`
replaces `result`, `--delete <run>` replaces `forget`, whose name hid a remote deletion, and
`--ls` lists every run and pushed branch left behind. Every refusal above still holds.

`run.yaml` runs `queue gate` itself instead of through `.github/actions/magus`, which
returns neither the output nor the exit code, and uploads both as the `command-output`
artifact the script reads.
