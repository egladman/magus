---
name: land-pull-requests
description: Land a batch of open pull requests in THIS repository through the magus merge queue, spending agent work only on the pull requests that need code changes. Use when asked to land, merge or ship open pull requests, or to keep a set of them moving until they reach main. tools/pull-requests.buzz decides each pull request's state, action and model; this skill routes to it and says who does what. Hand-authored for this repository and never shipped.
metadata:
  source: workspace
---

# Landing pull requests

`tools/pull-requests.buzz` decides; the orchestrator acts on what it prints. Do not
work out a state, an action or a model from `gh` yourself. When a record looks
wrong, fix the pure function that produced it and its test, then run it again.

## The loop

1. Read the board:

   ```sh
   ./magus buzz tools/pull-requests.buzz -- status [--pr <n>]... [--author <login>]
   ```

   With no selector it reads your own open pull requests. It prints one JSON
   record per pull request: `state`, `reason`, `action`, `model`, `evidence`.

2. Take the actions that change no code, with the same selectors:

   ```sh
   ./magus buzz tools/pull-requests.buzz -- apply [--pr <n>]... [--author <login>]
   ```

   It queues what is green on a current base and reruns a red that is not the
   change's, once per head, and prints each command it ran.

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
   --squash`, or for a stack the `queue: <method>` label on its top pull request.

5. Schedule the next pass with the host's wakeup, about every 20 minutes (the
   queue's validation time), or with its pull request monitor, and repeat from 1.
   A record with `action: none` needs nothing: it is queued, waiting on the pull
   request below it, or waiting on running checks. A `draft` or `closed` record
   never lands on its own; tell the person and drop it from the selection.

Stop when every selected record reads `merged`. That `status` output is the
proof; a green check or the queue's comment is not.

## Never

- Never admin-merge (`gh pr merge --admin`). It skips the queue's validation and
  its ordering, so nothing checked the combination that lands.
- Never force-push a pull request branch. Reviews and the queue's candidates name
  commits, and a rewrite orphans both.
- Never run two agents on one pull request branch at a time. One branch has one
  agent until you have pushed its result.
- Never rerun a red by hand after `apply` declined to. It reruns a head once; red
  again on the same head is a real failure, and the record then says `fix`.
