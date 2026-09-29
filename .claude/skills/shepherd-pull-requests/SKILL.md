---
name: shepherd-pull-requests
description: Shepherd a batch of open pull requests in THIS repository through the magus merge queue, spending agent work only on the pull requests that need code changes. Use when asked to shepherd, merge or ship open pull requests, or to keep a set of them moving until they reach main. hack/pull-requests.buzz decides each pull request's state, action and model; this skill routes to it and says who does what. Hand-authored for this repository and never shipped.
metadata:
  source: workspace
---

# Shepherding pull requests

`hack/pull-requests.buzz` decides; the orchestrator acts on what it prints. Do not
work out a state, an action or a model from `gh` yourself. When a record looks
wrong, fix the pure function that produced it and its test, then run it again.

## The loop

1. Read the pinned "Merge queue" issue first: every open pull request's place in
   the queue, with its reason and a person's command (`./magus buzz
   hack/pull-requests.buzz -- dashboard --all` renders it locally). Then read
   the board:

   ```sh
   ./magus buzz hack/pull-requests.buzz -- status [--pr <n>]... [--author <login>]
   ```

   With no selector it reads your own open pull requests; `--all` reads
   everyone's. It prints one JSON record per pull request: `state`, `stage`,
   `reason`, `action`, `model`, `evidence`, and sometimes `merge`.

   `state` is what the checks on the head say:

   - `running`: a check has not finished, or none has reported yet.
   - `green`: every check finished and passed.
   - `red-inherited`: every failing check and failing Go test also fails on
     main's latest completed CI run; `evidence` names both runs.
   - `red`: anything else; `evidence` names the failures main does not have.

   `stage` is what else stands before a merge: `merged`, `closed`, `draft`,
   `waiting-below`, `conflicting`, `kicked-back`, `behind`, `queued`,
   `needs-queue`.

   `merge` appears only on a `green` or `red-inherited` record whose stage
   allows a merge. It is the person's command; hand it over, never run it.

2. Take the actions that change no code, with the same selectors:

   ```sh
   ./magus buzz hack/pull-requests.buzz -- apply [--pr <n>]... [--author <login>]
   ```

   It queues what is `green` on a current base and reruns a `red` that is not
   the change's, once per head, and prints each command it ran. It never acts
   on `red-inherited`: nothing in the change is broken, and the queue would
   refuse it for main's failures.

3. For every record whose `model` is `sonnet` or `opus`, spawn one agent of that
   model:
   - in its own worktree cut from the pull request's branch (`head`), never in
     your checkout;
   - with the record's `action`, `reason` and `evidence` as its brief;
   - told to commit there and stop: it never pushes, queues or reruns.

   An opus agent may fan out sonnet agents for mechanical parts of its work (a
   regeneration, an import block), each in a worktree of its own.

4. When an agent reports, check its branch with a narrow test of what it changed.
   Then you, and only you, push it and queue it again: `gh pr merge <n> --auto
   --squash`, or for a stack the `merge-queue: <method>` label on its top pull
   request. A `queue: <state>` label is the queue's answer, never intent.

5. Schedule the next pass with the host's wakeup, about every 20 minutes (the
   queue's validation time), or with its pull request monitor, and repeat from 1.
   A record with `action: none` needs nothing from an agent: it is queued,
   waiting on the pull request below it, `running`, or `red-inherited` with a
   `merge` for the person. A `draft` or `closed` stage never merges on its own;
   tell the person and drop it from the selection.

Stop when every selected record's stage reads `merged`. That `status` output is the
proof; a green check or the queue's comment is not.

## Never

- Never admin-merge (`gh pr merge --admin`) yourself. It skips the queue's
  validation and its ordering, so nothing checked the combination that merges;
  the record's `merge` is for the person to run.
- Never force-push a pull request branch. Reviews and the queue's candidates name
  commits, and a rewrite orphans both.
- Never run two agents on one pull request branch at a time. One branch has one
  agent until you have pushed its result.
- Never rerun a red by hand after `apply` declined to. It reruns a head once; red
  again on the same head is a real failure, and the record then says `fix`.
