---
title: Merge queue
description: libs/mergequeue is a speculative, partitioned merge queue with its own CLI; magus only supplies it affected sets.
tags: [merge-queue, queue, pull-request, auto-merge, speculation, github, provider, mergequeue]
---

# Merge queue

The merge queue is a separate Go module, `github.com/egladman/magus/libs/mergequeue`,
with its own CLI, `mergequeue`. It does not import magus, and magus does not import it:
a queue owns checkouts (staging commits, landing) and magus never does. Anyone can run it
without magus. Magus feeds it one fact, each change's affected set, through
`magus affected <target> --plan --stdin`.

A pull request joins the queue when someone enables GitHub's auto-merge on it. From
there the queue:

- stages main plus every change ahead of it in its partition, then gates up to `--depth`
  of those stages at once, each running only what its own change adds;
- keeps changes whose affected sets are disjoint in separate partitions, which never wait
  on each other;
- lands each green change as its own commit, by its author, with the merge method the
  author picked, as soon as every change beneath it in its partition has landed;
- checks after each merge that main carries exactly the tree it validated, and stops if
  it does not;
- regenerates derived files a change touched or conflicted in, and pushes that
  regeneration to the change's branch with the change's author as author;
- refuses pull requests from forks.

## Commands

Every command writes JSONL events (`mergequeue.event/v1`) on stdout. Hook output and
errors go to stderr. A usage mistake exits 2; a queue that ran and failed exits 1.

| Command    | Reads                                  | Writes                              | Rights                      |
| ---------- | -------------------------------------- | ----------------------------------- | --------------------------- |
| `list`     | the provider                           | a `mergequeue.changes/v1` document  | read                        |
| `plan`     | changes (stdin or `--changes`)         | a `mergequeue.plan/v1` file         | read                        |
| `validate` | the plan                               | one `mergequeue.stage/v1` per change | read; runs the changes' code |
| `land`     | the plan and the verdicts as they come | merges through the provider         | write; runs no change's code |

```sh
mergequeue list --provider github --base main > changes.json
mergequeue plan --changes changes.json --provider github --out plan.json \
  --affected 'magus affected ci --plan --stdin'
mergequeue validate --plan plan.json --out stages \
  --gate 'magus affected ci --base "$MERGEQUEUE_BELOW" --no-default-charms' \
  --regenerate 'magus affected generate:rw --base "$MERGEQUEUE_BELOW"'
mergequeue land --plan plan.json --stages stages --provider github --follow
```

`plan` checks each change's approval at its exact head, drops what conflicts with main on
its own (kicked back with the conflicting files and the commits that touched them), and
partitions the rest. `validate --only <id>` validates one change: the changes beneath it
in its partition are staged under it but not gated, which is how a CI system spreads one
plan over separate jobs.

## Input: `mergequeue.changes/v1`

```json
{
  "schema": "mergequeue.changes/v1",
  "base": "main",
  "remote": "https://github.com/acme/acme",
  "changes": [
    {
      "id": "482",
      "head": "4b1c...",
      "ref": "refs/pull/482/head",
      "branch": "fix-parser",
      "repo": "acme/acme",
      "title": "fix: parser",
      "author": "priya",
      "fork": false,
      "affected": ["libs/parser", "apps/web"],
      "unbounded": ""
    }
  ]
}
```

`id` and `head` are required. Changes are in queue order. `affected` is the set of units
(projects, packages, anything the caller partitions by) the change can reach. Omitted or
`null` means unknown, and `unbounded` names why a set is not a proof; either one puts
the change in one partition with everything. An empty list is a proof that the change
reaches nothing.

## The affected hook

A change without an `affected` set is asked about with `--affected <command>`: the
command runs in the checkout with the change's paths on stdin, one per line, and prints a
JSON object with `affected` and optionally `unbounded`. Other keys are ignored, so magus's
plan output is the answer as it stands:

```sh
printf 'libs/parser/lex.go\n' | magus affected ci --plan --stdin
# {"count": 1, ..., "affected": ["apps/web", "libs/parser"], "unbounded": ""}
```

Magus sets `unbounded` when the paths edit the declarations the closure was computed
from (any `.buzz` file, `magus.yaml`, `magus.lock`), when no project claims them, or when
it could not diff and fell back to every project.

## Hooks on each staging commit

`--gate` and `--regenerate` run with `sh -c` in the staging commit's checkout, with:

| Variable              | Value                                   |
| --------------------- | --------------------------------------- |
| `MERGEQUEUE_CHANGE`   | the change's id                         |
| `MERGEQUEUE_HEAD`     | the change's head commit                |
| `MERGEQUEUE_BASE`     | the branch the queue merges into        |
| `MERGEQUEUE_BASE_SHA` | the commit every stage is built on      |
| `MERGEQUEUE_BELOW`    | the commit this stage was built on      |
| `MERGEQUEUE_STAGE`    | the staging commit (gate only)          |

The gate is green on exit 0 and red on any other exit except 75 (`EX_TEMPFAIL`, which
magus returns when a lock or the machine budget is busy): that one is run again, twice,
and then stops the run rather than kicking the author back. Gating against `$MERGEQUEUE_BELOW` runs only what the top
change adds, and the magus cache replays what the stage below already ran. Each stage is
a worktree of its own, so export one `MAGUS_CACHE_DIR` for all of them.

A file is derived when main's `.gitattributes` sets `linguist-generated` on it (change it
with `--attribute`); magus writes those lines for every declared output. Attributes are
read at the base commit, never from a change. A derived-file conflict takes the change's
side and `--regenerate` rewrites it, with the derived paths on stdin; the stage refuses a
regeneration that writes anything not derived.

## Verdicts: `mergequeue.stage/v1`

`validate --out <dir>` writes one directory per decided change, named by its escaped id,
holding `stage.json` and, for a green change, `stage.bundle` (the staging commits as a git
bundle). Each appears by rename the moment its change is decided, so a reader never sees a
partial one. `.done` beside them says the run finished.

```json
{
  "schema": "mergequeue.stage/v1",
  "base_sha": "9f2e...",
  "change": {"id": "483", "head": "c0de...", "title": "feat: lexer"},
  "decision": "land",
  "after": "482",
  "stage": "77aa...",
  "message": "* add lexer",
  "depth": 2,
  "duration_ms": 41230
}
```

`decision` is `land`, `kick` (with a `report` for the author) or `wait` (with a
`reason`). `after` is the change validated beneath this one. `land --follow` polls the
directory, and lands a change once its own verdict is green and `after` has landed.

## Landing as each stage goes green

Validation runs pull-request code with a read-only token; landing holds the write token
and runs none. They are two workflows, and verdicts pass between them one stage at a
time:

1. `queue.yaml` (read-only) plans, then fans the plan out as a job matrix, one
   `validate --only <id>` job per change up to the depth of each partition. Each job
   uploads its verdict as an artifact the moment it finishes.
2. `queue-land.yaml` starts when validation is requested (`workflow_run: requested`), from
   main's definition with its own secret, and downloads each verdict artifact as it
   appears, while validation is still running. `land --follow` lands each change whose
   predecessors have landed; `.done` is written once the validation run completes.

Why not have validation post a `magus/queue` status per stage and trigger landing on
the status event? Posting a status needs `statuses: write` in the job that runs
pull-request code, and branch protection requires exactly that status, so a pull
request could mark itself green. Statuses posted with the Actions token do not start
workflows either. A `workflow_run: completed` trigger fires once per run, so landing
would wait for the slowest stage. Artifacts are readable through the API as soon as they
are uploaded, which is what lets the write side follow the read side stage by stage
without either one holding the other's rights.

## Providers

A provider is a Buzz script run on an embedded gopherbuzz VM. It exports five functions,
each taking one record:

| Function       | Receives                                         | Returns                                               |
| -------------- | ------------------------------------------------ | ----------------------------------------------------- |
| `list_queue`   | `{base, remote}`                                 | a list of change records                              |
| `approval_at`  | the change plus `{sha}`                          | `{approved, head, required, approvals, reason}`       |
| `post_status`  | the change plus `{sha, context, state, description}` | `true` when recorded                              |
| `merge_change` | the change plus `{sha, message}`                 | `{merged, reason}`                                    |
| `kick_back`    | the change plus `{sha, body}`                    | `true` when both the comment and the removal happened |

All five are required. Scripts see Buzz's standard library and a `mergequeue` module
whose `request(method, url: .., body: .., headers: ..)` returns `{status, body}`.
`--provider github` is built in; `--provider path/to/provider.buzz` loads any other.
The GitHub provider reads `GITHUB_TOKEN` or `MERGEQUEUE_TOKEN` for its reads and only
`MERGEQUEUE_TOKEN` for its writes, so the read-only job cannot write by accident.

Merge intent is GitHub's native auto-merge. Kicking a change back comments and disables
auto-merge; enabling it again re-queues the change.
