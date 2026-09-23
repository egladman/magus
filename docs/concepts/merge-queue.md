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

It kicks an author back only for what their own change did: a real conflict, a red gate
on a stage whose every change beneath is validated, or a regeneration their code broke.
A machine failure (a gate killed by a signal, a lock held past its retries) stops the
run and leaves the change queued.

## Vocabulary

| Term        | Meaning                                                               |
| ----------- | --------------------------------------------------------------------- |
| base        | the branch the queue merges into                                      |
| base commit | the base's tip when the plan was made; every partition starts on it   |
| stage       | a staging commit: one change merged onto the stage beneath it         |
| onto        | the commit a stage was built onto: the base commit or the stage below |
| tip         | the base's commit at landing                                          |
| head        | a change's own commit                                                 |
| verdict     | what the queue decided for one change: `land`, `kick` or `wait`       |

## Commands

Every command writes JSONL events (`mergequeue.event/v1`) on stdout. Hook output and
errors go to stderr. A usage mistake exits 2; a queue that ran and failed exits 1.

| Command    | Reads                                             | Writes                                              | Rights                       |
| ---------- | ------------------------------------------------- | --------------------------------------------------- | ---------------------------- |
| `ls`       | the provider                                      | a `mergequeue.changes/v1` document                  | read                         |
| `plan`     | changes (stdin or `--changes`)                    | a `mergequeue.plan/v1` file                         | read                         |
| `validate` | the plan                                          | the plan and one `mergequeue.verdict/v1` per change | read; runs the changes' code |
| `apply`    | a source: the plan, then verdicts as they come    | merges through the provider                         | write; runs no change's code |

```sh
mergequeue ls --provider github --base main > changes.json
mergequeue plan --changes changes.json --provider github --out plan.json \
  --affected 'magus affected ci --plan --stdin'
mergequeue validate --plan plan.json --verdicts verdicts \
  --gate 'magus affected ci --base "$MERGEQUEUE_ONTO" --no-default-charms' \
  --regenerate 'magus affected generate:rw --base "$MERGEQUEUE_ONTO"'
mergequeue apply --provider github verdicts
```

`-C <path>`, the one global flag, goes before the command, as with git: the command
runs as if started in `<path>`, which is the git checkout the queue works in and what
every relative path resolves against. Every other flag belongs to the commands that use
it, and a flag a command does not take is a usage error. `validate` takes no
`--provider`: it runs the changes' code, so it never talks to the forge.

`plan` checks each change's approval at its exact head, drops what conflicts with main on
its own (kicked back with the conflicting files and the commits that touched them), and
partitions the rest. `validate --only <id>` validates one change: the changes beneath it
in its partition are staged under it but not gated, which is how a CI system spreads one
plan over separate jobs; a red there waits rather than kicks back, since it may be a
change beneath it that failed. `validate --parallel` caps the stages built or gated at
once across every partition.

`apply <source>` reads the plan and the verdicts from one source, which carries both:

| Source                                    | What it reads                                   |
| ----------------------------------------- | ----------------------------------------------- |
| `<dir>`                                   | the directory `validate --verdicts` writes      |
| `github-actions:<owner>/<name>/runs/<id>` | the artifacts GitHub Actions run `<id>` uploads |

When the text before the first `:` reads as a URL scheme of two or more characters, the
source is a scheme, and a scheme other than `github-actions` is a usage error. Write
`./x:y` for a directory whose name would read as one; a one-letter scheme is a Windows
drive, so `C:\queue` is a directory.

`apply` follows its source until the source is complete: a directory once `.done`
appears in it, a run once it has completed. `--once` applies what the source holds now
and stops, leaving the rest queued; `--interval` sets how often a follow reads the source
and is refused with `--once`.

A run source downloads the run's `magus-queue-plan` artifact, then each
`magus-queue-verdict-<id>` artifact as the run uploads it, and lands while slower stages
are still going. It reads the run's status before each listing, so it stops following
only after a listing made once the run had completed. It reads with `MERGEQUEUE_TOKEN`,
else `GITHUB_TOKEN`.

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
      "unbounded_by": ""
    }
  ]
}
```

`id` and `head` are required. An id is letters, digits, `.`, `_` and `-`, not starting
with `.` or `-`, since it names a directory; `head` is a full commit id, and `ref` and
`branch` must be names git accepts. Changes are in queue order. `affected` is the set of
units (projects, packages, anything the caller partitions by) the change can reach.
Omitted or `null` means unknown, and `unbounded_by` names why a set is not a proof;
either one puts the change in one partition with everything. An empty list is a proof
that the change reaches nothing.

## The affected hook

A change without an `affected` set is asked about with `--affected <command>`: the
command runs in the checkout with the change's paths on stdin, one per line, and prints a
JSON object with `affected` and optionally `unbounded_by`. Other keys are ignored, so
magus's plan output is the answer as it stands:

```sh
printf 'libs/parser/lex.go\n' | magus affected ci --plan --stdin
# {"count": 1, ..., "affected": ["apps/web", "libs/parser"]}
```

Magus sets `unbounded_by` when any path is one no project claims, when a path can move
the graph's edges or every project's build without seeding what it reaches (any `.buzz`
file, `magus.yaml` or `.magus.yaml`, `magus.lock`, a dependency manifest or lockfile a
spell declares, a workspace provider's declared inputs, a toolchain pin or a rule set),
and when it could not diff and fell back to every project.

## Hooks on each staging commit

`--gate` and `--regenerate` run with `sh -c`, in a process group of their own, in the
staging commit's checkout, with:

| Variable                 | Value                                |
| ------------------------ | ------------------------------------ |
| `MERGEQUEUE_CHANGE`      | the change's id                      |
| `MERGEQUEUE_HEAD`        | the change's head commit             |
| `MERGEQUEUE_BASE`        | the branch the queue merges into     |
| `MERGEQUEUE_BASE_COMMIT` | the commit every partition starts on |
| `MERGEQUEUE_ONTO`        | the commit this stage was built onto |
| `MERGEQUEUE_STAGE`       | the staging commit (gate only)       |

Hooks never see `MERGEQUEUE_TOKEN`, `GITHUB_TOKEN`, `GH_TOKEN` or the Actions runtime
tokens: a gate runs the changes' code.

The gate is green on exit 0 and red on any other normal exit. Exit 75 (`EX_TEMPFAIL`,
which magus returns when a lock or the machine budget is busy) is run again, twice, and
then stops the run rather than kicking the author back; so does a gate killed by a
signal, or exiting 130, 137 or 143, a shell's report of one. Gating against
`$MERGEQUEUE_ONTO` runs only what the top change adds, and the magus cache replays what
the stage below already ran. Each stage is a worktree of its own, so export one
`MAGUS_CACHE_DIR` for all of them.

A file is derived when main's `.gitattributes` sets `linguist-generated` on it (change it
with `--attribute`); magus writes those lines for every declared output. Attributes are
read at the base commit, never from a change. A derived-file conflict takes the change's
side and `--regenerate` rewrites it, with the derived paths on stdin. A regeneration that
exits non-zero, or writes anything not derived, kicks that change back and the run goes
on.

## Verdicts: `mergequeue.verdict/v1`

`validate --verdicts <dir>` first writes the plan there as `plan.json`, then one
directory per decided change, named by its id, holding `verdict.json` and, for a green
change, `stage.bundle` (the staging commits as a git bundle). Each appears by rename the
moment its change is decided, so a reader never sees a partial one. `.done` beside them
says the run finished, and a full run writes it even when it stopped early. Validating a
different plan into a directory that already holds one is an error.

```json
{
  "schema": "mergequeue.verdict/v1",
  "base_commit": "9f2e...",
  "change": {"id": "483", "head": "c0de...", "title": "feat: lexer"},
  "decision": "land",
  "after": "482",
  "onto": "5e1a...",
  "stage": "77aa...",
  "message": "* add lexer",
  "depth": 2,
  "duration_ms": 41230
}
```

`decision` is `land`, `kick` (with a `report` for the author) or `wait` (with a
`reason`). `after` is the change validated beneath this one and `onto` its stage.
`apply` polls the directory, and lands a change once its own verdict is green and
`after` has landed. It trusts a verdict only as far as the plan vouches for it: a head,
an `after` or an `onto` the plan does not match lands nothing.

## Landing as each stage goes green

Validation runs pull-request code with a read-only token; landing holds the write token
and runs none. They are two workflows, and verdicts pass between them one stage at a
time:

1. `queue.yaml` (read-only) plans, then fans the plan out as a job matrix, one
   `validate --only <id>` job per change up to the depth of each partition. Each job
   uploads its verdict as an artifact the moment it finishes.
2. `queue-land.yaml` starts when validation is requested (`workflow_run: requested`), from
   main's definition with a write-scoped Actions token, and downloads each verdict
   artifact as it appears, while validation is still running: `apply` with the run as its
   source lands each change whose predecessors have landed, and stops once the
   validation run completes.

The landing token is the job's own, so no long-lived secret exists. A merge made with
the Actions token starts no workflow, so once anything lands the job dispatches main's
CI, CD and the queue's next run itself.

Before each merge, landing predicts the tree main will carry. A file that both the stage
and something landed since the stage was built changed, such as a root index two
disjoint partitions both regenerate, is a combination nobody validated: the change waits
and is restaged on the next run.

The queue's status reads `pending` while it asks for a merge and `success` only once the
change has merged. A required status that went green first would let anyone merge the
change onto whatever main had become, so branch protection must let the GitHub Actions
app bypass that status; nothing else can satisfy it.

Why not have validation post a `magus/queue` status per stage and trigger landing on
the status event? Posting a status needs `statuses: write` in the job that runs
pull-request code, and branch protection requires exactly that status, so a pull
request could mark itself green. Statuses posted with the Actions token do not start
workflows either. A `workflow_run: completed` trigger fires once per run, so landing
would wait for the slowest stage. Artifacts are readable through the API as soon as they
are uploaded, which is what lets the write side follow the read side stage by stage
without either one holding the other's rights.

When landing has to push a regeneration, it pushes a landing commit onto the change's
branch with a lease on the validated head, so a branch that moved or was deleted is
left alone. If the merge then fails, the landing commit is the branch's head; a review
of the commit beneath it still covers it, because it adds only main and regenerated
files. The same holds for main merged into a change by its author.

## Providers

A provider is a Buzz script run on an embedded gopherbuzz VM. It exports five functions,
each taking one record:

| Function       | Receives                                                | Returns                                               |
| -------------- | ------------------------------------------------------- | ----------------------------------------------------- |
| `list_changes` | `{base, remote}`                                        | a list of change records                              |
| `approval_at`  | the change plus `{commit}`                              | `{approved, head, reason}`; `head` is required        |
| `post_status`  | the change plus `{commit, context, state, description}` | `true` when recorded                                  |
| `merge_change` | the change plus `{commit, message}`                     | `{merged, reason}`                                    |
| `kick_back`    | the change plus `{commit, report}`                      | `true` when both the comment and the removal happened |

All five are required. Scripts see Buzz's standard library and a `mergequeue` module
whose `request(method, url: .., body: .., headers: ..)` returns `{status, body}`.
`--provider github` is built in; `--provider path/to/provider.buzz` loads any other.
The GitHub provider reads `GITHUB_TOKEN` or `MERGEQUEUE_TOKEN` for its reads and only
`MERGEQUEUE_TOKEN` for its writes, so the read-only job cannot write by accident.

Merge intent is GitHub's native auto-merge. Kicking a change back comments and disables
auto-merge; enabling it again re-queues the change.
