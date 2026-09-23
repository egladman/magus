---
title: Merge queue
description: libs/mergequeue is a speculative, partitioned merge queue with its own CLI; magus supplies its version control operations and its affected sets.
tags: [merge-queue, queue, pull-request, auto-merge, speculation, stacks, github, provider, mergequeue]
---

# Merge queue

The merge queue is a separate Go module, `github.com/egladman/magus/libs/mergequeue`,
with its own CLI, `mergequeue`. Magus does not import it: a queue owns checkouts
(candidates, applying) and magus never does. The queue's root package imports nothing of
magus either; it asks its host through small interfaces, and its `client` package
answers them with magus: version control through magus's `vcs` package (git today; jj,
Mercurial and Sapling refuse by name), and each change's affected set through magus's Go
SDK, with the workspace loaded once. Another build tool answers through
`plan --affected <command>`.

A pull request joins the queue when someone enables GitHub's auto-merge on it, or, for a
stack of pull requests, when a `queue: <method>` label is on the top one. From there the
queue:

- builds a candidate per change, main plus every change ahead of it in its partition,
  then gates up to `--depth` of them at once, each running only what its own change adds;
- keeps changes whose affected sets are disjoint in separate partitions, which never wait
  on each other;
- lands a change stacked on another after it, with its own delta measured from that
  change's head;
- merges each green change as its own commit, by its author, with the merge method the
  author picked, as soon as every change beneath it in its partition has merged;
- checks after each merge that main carries exactly the tree it validated, in the shape
  the merge method gives, and stops if it does not;
- regenerates generated files a change touched or conflicted in, and pushes an update
  commit to the change's branch with the change's author as author;
- refuses pull requests from forks.

It kicks an author back only for what their own change did: a real conflict, a red gate
on a candidate whose every change beneath is validated, a regeneration their code broke,
or a stack it cannot land. A machine failure (a gate killed by a signal, a lock held past
its retries) stops the run and leaves the change queued.

## Vocabulary

| Term         | Meaning                                                                   |
| ------------ | ------------------------------------------------------------------------- |
| base         | the branch the queue merges into                                          |
| base commit  | the base's tip when the plan was made; every partition starts on it       |
| candidate    | a speculative merge commit: one change merged onto the candidate beneath  |
| onto         | the commit a candidate was built onto: the base commit or the one below   |
| tip          | the base's commit at apply                                                |
| head         | a change's own commit                                                     |
| stack base   | the head of the change a change is stacked on; its own delta starts there |
| merge method | `merge`, `squash` or `rebase`: how the provider lands the change          |
| verdict      | what the queue decided for one change: `merge`, `kick` or `wait`          |
| code         | why a change waits or was kicked back, from a closed set                  |

## Commands

Every command writes JSONL events (`mergequeue.event/v1`) on stdout. Hook output and
errors go to stderr. A usage mistake exits 2; a queue that ran and failed exits 1.

| Command    | Reads                                          | Writes                                              | Rights                       |
| ---------- | ---------------------------------------------- | --------------------------------------------------- | ---------------------------- |
| `ls`       | the provider                                   | a `mergequeue.changes/v1` document                  | read                         |
| `plan`     | changes (stdin or `--changes`)                 | a `mergequeue.plan/v1` file                         | read                         |
| `validate` | the plan                                       | the plan and one `mergequeue.verdict/v1` per change | read; runs the changes' code |
| `apply`    | a source: the plan, then verdicts as they come | merges through the provider                         | write; runs no change's code |

```sh
mergequeue ls --provider github --base main > changes.json
mergequeue plan --changes changes.json --provider github --out plan.json
mergequeue validate --plan plan.json --verdicts verdicts \
  --gate 'magus affected ci --base "$MERGEQUEUE_ONTO" --no-default-charms' \
  --regenerate 'magus affected generate:rw --base "$MERGEQUEUE_ONTO"'
mergequeue apply --provider github verdicts
```

`-C <path>`, the one global flag, goes before the command, as with git: the command
runs as if started in `<path>`, which is the checkout the queue works in and what every
relative path resolves against. Every other flag belongs to the commands that use
it, and a flag a command does not take is a usage error. `validate` takes no
`--provider`: it runs the changes' code, so it never talks to one. `--remote` names a
remote configured in the checkout (`origin` unless given); a URL is refused.

`plan` checks each change's approval at the commit a review of its head covers, finds
which changes are stacked on which, drops what conflicts with main on its own (kicked
back with the conflicting files and the commits that touched them), and partitions the
rest. `validate --only <id>` validates one change: the changes beneath it in its
partition are merged under it but not gated, which is how a CI system spreads one plan
over separate jobs; a red there waits rather than kicks back, since it may be a change
beneath it that failed. `validate --parallel` caps the candidates built or gated at once
across every partition.

`apply <source>` reads the plan and the verdicts from one source, which carries both:

| Source      | What it reads                                                       |
| ----------- | ------------------------------------------------------------------- |
| `<dir>`     | the directory `validate --verdicts` writes                          |
| `run:<run>` | the artifacts of a validation run, `<run>` as the provider names it |

The GitHub provider names a run `<owner>/<name>/runs/<id>`. When the text before the
first `:` reads as a URL scheme of two or more characters, the source is a scheme, and a
scheme other than `run` is a usage error. Write `./x:y` for a directory whose name would
read as one; a one-letter scheme is a Windows drive, so `C:\queue` is a directory.

`apply` follows its source until the source is complete: a directory once `.done`
appears in it, a run once it has completed. `--once` applies what the source holds now
and stops, leaving the rest queued; `--interval` sets how often a follow reads the source
and is refused with `--once`. `--committer "Name <email>"` commits each update commit the
queue pushes; its author is always the change head's author.

A run source asks the provider's `run_artifacts` for the run's `magus-queue-plan`
artifact, then for each `magus-queue-verdict-<id>` artifact as the run uploads it, and
merges while slower candidates are still validating. The provider reads the run's status
before each listing, so apply stops following only after a listing made once the run had
completed. It downloads each artifact with the headers the provider returns, and drops
them all on a redirect to another host. A provider without `run_artifacts` cannot be
followed through a run, and `apply` says so before reading anything.

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
      "method": "squash",
      "parent": "481",
      "fork": false,
      "affected": ["libs/parser", "apps/web"],
      "unbounded_by": ""
    }
  ],
  "landed": [
    {"id": "479", "head": "9f8e...", "commit": "3f9b...", "method": "squash"}
  ]
}
```

`id`, `head` and `method` are required. An id is letters, digits, `.`, `_` and `-`, not
starting with `.` or `-`, since it names a directory; `head` is a full commit id, and
`ref` and `branch` must be names git's `check-ref-format` accepts. `method` is `merge`,
`squash` or `rebase`: a provider's default the queue cannot see is no method. `parent` is
the change the provider says this one is stacked on, a hint the plan checks against
ancestry. Changes are in queue order. `affected` is the set of units (projects, packages,
anything the caller partitions by) the change can reach. Omitted or `null` means
unknown, and `unbounded_by` names why a set is not a proof; either one puts the change in
one partition with everything. An empty list is a proof that the change reaches nothing.

`landed` lists recently merged changes an open one may be stacked on. Planning checks
that main carries each one's `commit` and refuses the whole input when it does not.

## Stacks

A change is stacked on another when it carries that change's head and main does not:
the other is open in the queue, or landed as a squash or a rebase, which leaves its head
off main. A merge of main into the change beneath (GitHub's "Update branch", or an
update commit the queue pushed) does not break the stack. The nearest such change is the
change's stack base, and its own delta is measured from there:

- it lands after the change beneath it, in the same partition whatever their keys;
- its candidate is built from its stack base, so a line it deleted from the change
  beneath stays deleted after that change lands as a squash, where the plain merge from
  its fork point would bring it back;
- when the change beneath is kicked back, or waits, it waits too and is never blamed:
  `WAIT_PARENT_KICKED` or `WAIT_PARENT`.

The provider's `parent` must agree with ancestry. A change declaring a parent it is not
built on waits with `WAIT_RESTACK`; one declaring a parent the queue does not list waits
with `WAIT_PARENT`. Refused, with `KICK_REFUSED`:

- a stack mixing merge methods, since a squashed change beneath a merged one lands twice
  in `git log`;
- a stacked change with the rebase method, until predicting a rebase no longer needs git's
  experimental `replay`;
- a change built on two queued changes neither of which is built on the other;
- a stack more than 16 unlanded changes deep.

A change stacked on a squashed one usually lands through an update commit whose tree is
the validated one: the tip plus the change's own delta, which is the diff its reviewers
approved. Where the provider needs stacked branches linear, the update commit sits on the
tip alone. An approval carries over a rebase that only moved the change: its whole diff,
replayed onto the new base without a conflict, is exactly the new head.

Where the provider lands stacks atomically, a validated run of changes stacked on each
other lands in one call through its top, when landing each one's own delta gives each
one's validated tree. The queue then checks every member's tree and shape, and stops when
the provider landed only part of the run.

## Hooks on each candidate

`--gate` and `--regenerate` run with `sh -c`, in a process group of their own, in the
candidate's checkout, with:

| Variable                 | Value                                    |
| ------------------------ | ---------------------------------------- |
| `MERGEQUEUE_CHANGE`      | the change's id                          |
| `MERGEQUEUE_HEAD`        | the change's head commit                 |
| `MERGEQUEUE_BASE`        | the branch the queue merges into         |
| `MERGEQUEUE_BASE_COMMIT` | the commit every partition starts on     |
| `MERGEQUEUE_ONTO`        | the commit this candidate was built onto |
| `MERGEQUEUE_CANDIDATE`   | the candidate (gate only)                |

Hooks never see `MERGEQUEUE_TOKEN`, `GITHUB_TOKEN`, `GH_TOKEN` or the Actions runtime
tokens: a gate runs the changes' code.

The gate is green on exit 0 and red on any other normal exit. Exit 75 (`EX_TEMPFAIL`,
which magus returns when a lock or the machine budget is busy) is run again, twice, and
then stops the run rather than kicking the author back; so does a gate killed by a
signal, or exiting 130, 137 or 143, a shell's report of one. Gating against
`$MERGEQUEUE_ONTO` runs only what the top change adds, and the magus cache replays what
the candidate below already ran. Each candidate is a checkout of its own (a git worktree),
so export one `MAGUS_CACHE_DIR` for all of them.

A file is generated when main's `.gitattributes` sets `linguist-generated` on it; magus
writes those lines for every declared output. Attributes are read at the base commit,
never from a change. A generated-file conflict takes the change's side and
`--regenerate` rewrites it, with the generated paths on stdin; without a hook, a file
either side deleted stays deleted. A regeneration that exits non-zero, or writes anything
not generated, kicks that change back and the run goes on.

## What a review covers

Approval is checked at the commit a review of the head covers: the head itself, or,
walking back through merges of main into the change, the commit beneath each merge that
adds nothing a reviewer did not see. A merge adds nothing when its tree is the plain
merge of its parents (or, for a stacked change, their merge from its stack base). A merge
that differs from that only in generated files adds nothing only when regeneration
reproduces them: validation regenerates them in a checkout of the merge, and a merge whose
regeneration rewrites anything, or a run without `--regenerate`, leaves the change
waiting with `WAIT_NOT_APPROVED` for an approval at its head. A vendored tree marked
generated gets no exemption for the mark alone. The verdict records the commit validation
proved the review covers, and `apply` merges only on that proof.

## Verdicts: `mergequeue.verdict/v1`

`validate --verdicts <dir>` first writes the plan there as `plan.json`, then one
directory per decided change, named by its id, holding `verdict.json` and, for a green
change, `candidate.export` (the candidate's commits as the version control exports them,
a git bundle under git). Each appears by rename the moment its change is decided, so a
reader never sees a partial one. `.done` beside them says the run finished, and a full
run writes it even when it stopped early. Validating a different plan into a directory
that already holds one is an error.

```json
{
  "schema": "mergequeue.verdict/v1",
  "base_commit": "9f2e...",
  "change": {"id": "483", "head": "c0de...", "method": "squash", "title": "feat: lexer"},
  "decision": "merge",
  "after": "482",
  "onto": "5e1a...",
  "candidate": "77aa...",
  "method": "squash",
  "message": "* add lexer",
  "depth": 2,
  "duration_ms": 41230
}
```

`decision` is `merge`, `kick` (with a `report` for the author) or `wait` (with a
`reason`); every `kick` and `wait` carries a `code`, and `paths` and `with` name the files
at issue and the base commits that touched them. `after` is the change validated beneath
this one and `onto` its candidate; `method` is the merge method it was validated under,
and `reviewed` the commit a review of its head covers when proving that took
regeneration. `apply` polls the directory, and merges a change once its own verdict is
green and `after` has merged. It trusts a verdict only as far as the plan vouches for it:
a head, an `after` or an `onto` the plan does not match merges nothing.

### Codes

| Code                  | Means                                                               |
| --------------------- | ------------------------------------------------------------------- |
| `WAIT_NOT_APPROVED`   | no approval at the commit a review of its head covers               |
| `WAIT_HEAD_MOVED`     | its head moved since it was listed or validated                     |
| `WAIT_BEHIND`         | a change beneath it did not merge, or it was not validated this run |
| `WAIT_CONFLICT_AHEAD` | it conflicts with a change ahead of it, which merges first          |
| `WAIT_REVALIDATE`     | what it was validated on is no longer what it would merge onto      |
| `WAIT_BRANCH_MOVED`   | its branch moved or was deleted before an update commit could land  |
| `WAIT_HOST_REFUSED`   | the provider refused the merge                                      |
| `WAIT_MERGED`         | its head is already on the base                                     |
| `WAIT_PARENT`         | the change it is stacked on has not landed                          |
| `WAIT_PARENT_KICKED`  | the change it is stacked on was kicked back                         |
| `WAIT_RESTACK`        | it is not built on the head of the change it says it is stacked on  |
| `WAIT_RETARGET`       | it targets another branch than the queue's base                     |
| `WAIT_METHOD_CHANGED` | its merge method changed since validation                           |
| `KICK_CONFLICT`       | a real conflict with the base in files that are not generated       |
| `KICK_RED`            | the gate was red on its candidate                                   |
| `KICK_REFUSED`        | something the author has to fix that is neither                     |

## Applying as each candidate goes green

Validation runs pull-request code with a read-only token; apply holds the write token
and runs none. They are two workflows, and verdicts pass between them one change at a
time:

1. `queue.yaml` (read-only) plans, then fans the plan out as a job matrix, one
   `validate --only <id>` job per change up to the depth of each partition. Each job
   uploads its verdict as an artifact the moment it finishes.
2. `queue-apply.yaml` starts when validation is requested (`workflow_run: requested`), from
   main's definition with a write-scoped Actions token, and downloads each verdict
   artifact as it appears, while validation is still running: `apply` with the run as its
   source merges each change whose predecessors have merged, and stops once the
   validation run completes.

The apply token is the job's own, so no long-lived secret exists. A merge made with
the Actions token starts no workflow, so once anything merges the job dispatches main's
CI, CD and the queue's next run itself.

Before each merge, apply re-reads the change's approval, base and merge method from the
provider. A change targeting another branch is retargeted at main, since the provider
merges a change into its own base; a changed merge method waits for the next run. Apply
then predicts the tree main will carry: the candidate's changes since what it was built
onto, merged onto main. A file that both the candidate and something merged since it was
built changed, such as a root index two disjoint partitions both regenerate, is a
combination nobody validated: the change waits and is validated again on the next run.

The queue's status reads `pending` while it asks for a merge and `success` only once the
change has merged. A required status that went green first would let anyone merge the
change onto whatever main had become, so branch protection must let the GitHub Actions
app bypass that status; nothing else can satisfy it.

Why not have validation post a `magus/queue` status per candidate and trigger apply on
the status event? Posting a status needs `statuses: write` in the job that runs
pull-request code, and branch protection requires exactly that status, so a pull
request could mark itself green. Statuses posted with the Actions token do not start
workflows either. A `workflow_run: completed` trigger fires once per run, so apply
would wait for the slowest candidate. Artifacts are readable through the API as soon as
they are uploaded, which is what lets the write side follow the read side change by
change without either one holding the other's rights.

When the provider's own merge of a head would not land the validated tree, apply pushes
an update commit onto the change's branch with a lease on the validated head, so a branch
that moved or was deleted is left alone. It may differ from the provider's merge only in
generated files, or, for a change stacked on a squashed one, in exactly what that change
landed. Anything else is kicked back: merging it the way the provider would differs from
what was validated. After the merge, apply checks that main carries the validated tree
in the shape the merge method gives (one new commit for a squash, a merge commit whose
second parent is what it handed over, a line of commits for a rebase), and stops
otherwise.

## Providers

A provider is a Buzz script run on an embedded gopherbuzz VM. It exports these
functions, each taking one record:

| Function        | Receives                                                         | Returns                                               |
| --------------- | ---------------------------------------------------------------- | ----------------------------------------------------- |
| `describe`      | `{base, remote}`                                                 | `{stack_merge, linear_stacks, methods}`               |
| `list_changes`  | `{base, remote}`                                                 | `{changes, landed}`                                   |
| `approval_at`   | the change plus `{commit}`                                       | `{approved, head, reason, base, method, approved_at}` |
| `post_status`   | the change plus `{commit, context, state, description}`          | `true` when recorded                                  |
| `retarget`      | the change plus `{base}`                                         | `true` once the change targets `base`                 |
| `merge_change`  | the change plus `{commit, message, through}`                     | `{merged, reason}`                                    |
| `kick_back`     | the change plus `{commit, code, report, paths, with, candidate}` | `true` when both the comment and the removal happened |
| `run_artifacts` | `{run}`                                                          | `{completed, headers, artifacts: [{name, url}]}`      |

All but `run_artifacts` are required, and a script missing one is refused when it
opens; `run_artifacts` is required of a provider `apply` follows through a run.
`stack_merge` is `sequential` or `atomic`, `methods` the merge methods the repository
allows, and a change with any other method is refused. `approval_at` must name the
change's current head, base and merge method; `approved_at` names an older commit its
approvals stand at, which the queue carries over only across a rebase that changed
nothing. `through`, when set, is the lowest change of a stack run the call lands.

Scripts see Buzz's standard library and a `mergequeue` module whose
`request(method, url: .., body: .., headers: ..)` returns `{status, body}`. The records a
script receives hold strings, bools, and lists of strings. `--provider github` is built
in; `--provider path/to/provider.buzz` loads any other. The GitHub provider reads
`GITHUB_TOKEN` or `MERGEQUEUE_TOKEN` for its reads and only `MERGEQUEUE_TOKEN` for its
writes, so the read-only job cannot write by accident.

Merge intent is GitHub's native auto-merge, and, for a stack, a `queue: <method>` label
on its top pull request applied by someone who holds write access. Kicking a change back
comments with the report and one JSON line of its code and files, then disables
auto-merge or removes the label; queueing it again re-queues the change.

## The library

The queue is usable without magus. Its root package, `mergequeue`, holds the three steps
(`Planner`, `Validator`, `Applier`, each built with its required dependencies and run
with `Run`), the documents, the verdict directory, the run follower and the command
hooks, and asks its host through four interfaces:

| Interface    | What it answers                                                    | magus's implementation      |
| ------------ | ------------------------------------------------------------------ | --------------------------- |
| `VCS`        | revisions, trees and merges; checkouts, commits, a leased push     | `client.Repo`               |
| `BuildFacts` | a change's affected set, and why it is not a proof                 | `client.Workspace`          |
| `Provider`   | list, describe, approve, post a status, retarget, merge, kick back | the `provider` Buzz scripts |
| `RunReader`  | the artifacts a validation run uploaded                            | the `provider` Buzz scripts |

`VCS` holds facts and primitive writes only. Every merge, check and push is composed in
the queue from them, so which merge base a prediction takes, which conflicts are the
author's and which differences a review need not see are decided once, whatever the
version control. `CommandFacts` is the `BuildFacts` behind `--affected`.

Where Go ends and Buzz begins is a rule, not a taste. Go holds what the invariants are
proven over and what needs the machine: admission, partitioning, candidate order, the
landing checks, version control, processes, files and concurrency. Buzz holds what talks
to a system outside the repository, the provider and the CI system, as a pure function of
that system's answers and the record it was handed: it supplies facts and performs
writes, and decides nothing. A fact the queue acts on is re-checked in Go before it is
trusted: a change record passes `Change.Check`, an approval must name a head, a base and
a merge method, a declared stack parent must match ancestry, a stale head is unproven. A
knob is a fact, not a script: what the schedule computes over comes from the build tool
and the provider; how it computes is fixed in Go.
