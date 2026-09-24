---
title: Merge queue
description: magus queue is a speculative, partitioned merge queue; magus supplies its version control, its affected sets and which files are generated.
tags: [merge-queue, queue, magus queue, pull-request, auto-merge, speculation, stacks, github, provider, mergequeue]
---

# Merge queue

`magus queue` is a speculative, partitioned merge queue, compiled into magus the way
`magus buzz` is: one binary holds what it needs. Its code is `libs/mergequeue`, a
package tree of the magus module. The queue takes magus's version control capability
interfaces as they are, narrowed per step, and its `client` package wires magus in: the
version control backend the checkout names (git today; jj, Mercurial and Sapling refuse
by name), and each change's affected set and the workspace's declared outputs through
magus's Go SDK, with the workspace loaded once. Another build tool answers through
`--facts <command>`.

A change joins the queue when its provider reports merge intent on it. With the built-in
GitHub provider that is auto-merge enabled on a pull request, or, for a stack, a label
on its top made of the prefix `describe` reports and a merge method (`queue: squash`).
From there the queue:

- builds a candidate per change, main plus every change ahead of it in its partition,
  then gates up to `--depth` of them at once, each running only what its own change adds;
- keeps changes whose affected sets are disjoint in separate partitions, which never wait
  on each other;
- merges a change stacked on another after it, with its own delta measured from that
  change's head;
- rebuilds each green candidate in the job that holds the write credential, from the
  change's head and main's own regeneration, and merges it only when the rebuild is the
  commit validation gated;
- merges each green change as its own commit, by its author, with the merge method the
  author picked, as soon as every change beneath it in its partition has merged;
- checks after each merge that main carries exactly the tree it predicted, in the shape
  the merge method gives, and stops if it does not;
- refuses changes from forks, whose branches it cannot push to.

It kicks an author back only for what their own change did: a real conflict, a red gate
on a candidate whose every change beneath is validated and whose base is green on the
same projects, a regeneration their code broke,
generated files only they can regenerate, or a stack it cannot merge. A gate killed by a
signal, an OOM kill included, is that change's red. Only what the queue can prove is the
machine's (a hook that could not start, the queue's own cancellation) stops a partition
and leaves the change queued.

## Using it

On GitHub, queuing a pull request is enabling auto-merge on it, with the merge method
you want it to land with:

```sh
gh pr merge 482 --auto --squash
```

or "Enable auto-merge" on the pull request's page. Nothing else changes for the author:
push fixes as usual, and a push to a queued pull request has it validated again. Once
the queue's `merge-queue` status is main's required check, auto-merge cannot fire on its
own: GitHub waits for that status, and the queue sets it to `success` only when it is
about to see that pull request merged.

| To                           | Do                                                      |
| ---------------------------- | ------------------------------------------------------- |
| queue a pull request         | `gh pr merge <n> --auto --squash` (or `--rebase`)       |
| queue a stack                | label its top pull request `queue: squash`              |
| take it out                  | `gh pr merge <n> --disable-auto`, or remove the label   |
| list what is queued          | `magus queue ls --provider github --base main`          |
| tell one is queued           | it carries the label `merge-queue: queued`              |
| tell one was kicked back     | it carries the label `merge-queue: rejected`            |
| tell one changes a generator | it carries the label `merge-queue: changes a generator` |
| see why one is waiting       | its `merge-queue` status, which reads `waiting: <why>`  |
| see why one was kicked back  | the queue's newest comment on it                        |
| land it past the queue       | `gh pr merge <n> --admin`: an admin's bypass, see below |

A queued pull request carries `merge-queue: changes a generator` beside
`merge-queue: queued` when it touches generated files the queue cannot regenerate
itself: the build tool cannot prove their regeneration runs none of the pull request's
own code. Nothing is wrong yet, but when main moves those files the queue kicks it back,
and only you can merge main in and regenerate.

A pull request the queue kicks back gets a new comment, and its auto-merge or label is
removed. The comment says what failed on which commits, links the validation run, and
holds, collapsed, the files at issue and a block that runs the same validation on your
machine; its last line is the command that queues it again. One that waits (for a
review, for the change beneath it, for main to settle) stays queued and needs nothing.

An admin merge skips validation and ordering both. The queue notices on its next run
that main moved without it and plans again from the new tip, so nothing breaks, but
nothing checked the combination either. Keep it for when the queue itself is down.

## Trust model

Bytes produced by running a change's code are as untrusted as code its author typed.
Validation runs every change's code, the gate and the regeneration alike, so everything
it writes is a claim, never content: the job holding the write token never takes a
generated file, a candidate tree or a review proof from a verdict.

- **Apply rebuilds.** For each green verdict, apply builds the candidate again from the
  change's head onto what it rebuilt for the change beneath, with the same fixed
  identity and date validation used, and merges only when its rebuild is the commit
  validation gated. The verdict's candidate commit is a hash apply compares against.
- **Generated bytes come from main's regeneration, or from the author.** Where the
  candidate holds regenerated files (validation's regeneration rewrote something, or a
  generated file conflicted), apply runs main's own regeneration (`apply --regenerate`)
  in its rebuild, and only after the build tool proves the change touches none of the
  code that regeneration runs: the generating targets' definitions, their spell and op
  sources, toolchain pins and lockfiles, the magusfiles, and every code input those
  targets read. A change that fails the proof goes back to its author with the paths,
  who regenerates and pushes, and review covers the result. This is the only way
  generated bytes reach an author's branch.
- **No credential reaches a hook.** Hooks run with `MERGEQUEUE_TOKEN`, `GITHUB_TOKEN`,
  `GH_TOKEN` and the Actions runtime tokens removed from their environment; main's
  regeneration in the apply job runs inside magus's sandbox, which keeps the checkout's
  git config and the parent's environment out of its reach.
- **Generated means declared.** A file is generated when some target declares it as its
  output (`magus describe file` says `output`), read from main's declarations. A
  `linguist-generated` attribute alone makes nothing generated: a vendored tree so marked
  is source, and a reviewer has to see it. A file that `generate`, or a target it
  needs, rewrites in place (`magus describe file` says `declared: update`) stays
  source: regeneration may write it, but its conflicts are the author's and a review
  sees it. Another target's in-place update, such as a formatter's, does not count. A file magus maintains itself
  (`maintained`, such as `.gitattributes`) is rewritten by main's magus whenever a hook
  runs it, so the queue puts the change's version back and never commits main's.
- **A review is proven in apply.** Whether a review of an older commit covers a merge of
  main into the change is a version control question apply answers itself, and a merge
  differing only in generated files is covered only when main's regeneration, run on the
  plain merge, gives exactly that merge's tree.
- **Candidates share nothing.** Each candidate gets a checkout and a scratch directory
  of its own, so no change's hook can plant a cache entry another candidate's gate
  replays; `tools/gha-queue.buzz` points magus's and Go's caches there
  with `--scratch-env`. Every
  process a hook starts is killed when the hook exits, before its verdict is recorded; a
  process that leaves the hook's process group ends with the CI job.

How the provider's own credential is scoped is a separate question, answered under
[Providers](#providers).

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
| merge method | `merge`, `squash` or `rebase`: how the provider merges the change         |
| verdict      | what the queue decided: `merge`, `kick`, `wait`, or `merged` when done    |
| code         | why a change waits or was kicked back, from a closed set                  |

## Commands

Every subcommand writes JSONL events (`mergequeue.event/v1`) on stdout. Hook output and
errors go to stderr. A usage mistake exits 2; a queue that ran and failed exits 1.

| Subcommand | Reads                                          | Writes                                                                 | Rights                       |
| ---------- | ---------------------------------------------- | ---------------------------------------------------------------------- | ---------------------------- |
| `describe` | the provider                                   | setup steps, or a `mergequeue.capabilities/v1` document with `-o json` | read                         |
| `ls`       | the provider                                   | a `mergequeue.changes/v1` document                                     | read                         |
| `plan`     | changes (stdin or `--changes`)                 | a `mergequeue.plan/v1` file                                            | read                         |
| `validate` | the plan                                       | the plan and one `mergequeue.verdict/v1` per change                    | read; runs the changes' code |
| `apply`    | a source: the plan, then verdicts as they come | merges through the provider                                            | write; runs no change's code |

```sh
magus queue ls --provider github --base main > changes.json
magus queue plan --changes changes.json --provider github --out plan.json
magus queue validate --plan plan.json --verdicts verdicts \
  --gate 'magus run ci --no-default-charms' \
  --regenerate 'magus run generate:rw' \
  --scratch-env MAGUS_CACHE_DIR=magus --scratch-env GOCACHE=go-build
magus queue apply --provider github \
  --regenerate 'magus --sandbox-enabled run generate:rw' \
  --scratch-env MAGUS_CACHE_DIR=magus verdicts
```

`--scratch-env NAME=DIR`, on `validate` and `apply` and repeatable, sets `NAME` to `DIR`
inside the checkout's scratch directory in every hook's environment, creating the
directory. It keeps
each candidate's caches its own without the hook line saying so, which leaves the line
one a person can paste and run; each verdict records the lines validation ran.

The checkout the queue works in is the one at magus's global `--root` (`-C`), before the
subcommand as with git, and every relative path resolves against it. magus's other global
flags apply as everywhere; `--dry-run` makes `apply` report what would merge and call
nothing on the provider. `validate` takes no `--provider`: it runs the changes' code, so
it never talks to one. `plan` requires one, since it checks approval. `--remote` names a
remote configured in the checkout (`origin` unless given); a URL is refused. `--vcs`
names the backend (`git` unless given); the queue reads neither `MAGUS_VCS_ENABLED` nor
`MAGUS_VCS_NAME`, which configure magus's own use of version control. `magus queue` never
runs through the daemon: it acts on the caller's checkout.

`plan` checks each change's approval at the commit a review of its head covers, finds
which changes are stacked on which, drops what conflicts with main on its own (kicked
back with the conflicting files and the commits that touched them), and partitions the
rest. `validate --only <id>` validates one change: the changes beneath it in its
partition are merged under it but not gated, which is how a CI system spreads one plan
over separate jobs; a red there waits rather than kicks back, since it may be a change
beneath it that failed. `--parallel` caps the work done at once, and means the number of
CPUs when zero, for `plan` and `validate` alike.

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
and is refused with `--once`. Each update commit the queue pushes is committed by the
committer the provider's `describe` names; `--committer "Name <email>"` overrides it,
for example `--committer "Release Bot <release-bot@example.com>"`. magus has no default:
with neither, a change that needs an update commit waits with `WAIT_NO_COMMITTER` and
apply stops with an error. `--app <slug>` names the app whose token the provider writes
with, when it is not the job's own; apply then checks that the base requires the queue's
status from that app.

A run source asks the provider's `list_artifacts` for the run's `mergequeue-plan`
artifact, then for each `mergequeue-verdict-<id>` artifact as the run uploads it, and
merges while slower candidates are still validating. The provider reads the run's status
before each listing, so apply stops following only after a listing made once the run had
completed. It downloads over https alone, with the headers the provider returns, drops
them all on a redirect to another host, and refuses a redirect away from https. A
download past 32 MiB, an archive past 64 entries or 64 MiB unpacked, an entry that is
not a regular file inside the artifact, or a verdict that does not read, holds that one
change and nothing else. A provider without `list_artifacts` cannot be followed through
a run, and `apply` says so before reading anything.

## Input: `mergequeue.changes/v1`

```json
{
  "schema": "mergequeue.changes/v1",
  "base": "main",
  "remote_url": "https://github.com/acme/acme",
  "changes": [
    {
      "id": "482",
      "repo": "acme/acme",
      "head": "4b1c...",
      "ref": "refs/pull/482/head",
      "branch": "fix-parser",
      "base": "main",
      "title": "fix: parser",
      "method": "squash",
      "parent": "481",
      "fork": false,
      "affected": ["libs/parser", "apps/web"],
      "unbounded_by": ""
    }
  ],
  "merged": [
    {"id": "479", "head": "9f8e...", "commit": "3f9b...", "method": "squash"}
  ],
  "unqueued": [
    {"id": "490", "repo": "acme/acme", "head": "a1b2...", "mark": "queued"}
  ]
}
```

`id`, `head`, `base` and `method` are required, and two changes never share a head. An
id is letters, digits, `.`, `_` and `-`, not starting with `.` or `-`, since it names a
directory; `head` is a full commit id, and `ref`, `branch` and `base` must be names git's
`check-ref-format` accepts. `method` is `merge`, `squash` or `rebase`: a provider's
default the queue cannot see is no method. `parent` is the change the provider says this
one is stacked on, a hint the plan checks against ancestry. Changes are in queue order.
`affected` is the set of units (projects, packages, anything the caller partitions by)
the change can reach. Omitted or `null` means unknown, and `unbounded_by` names why a set
is not a proof; either one puts the change in one partition with everything. An empty
list is a proof that the change reaches nothing.

`merged` lists the merged changes whose head a queued change carries. Planning checks
that main carries each one's `commit` and refuses the whole input when it does not.
`unqueued` lists every other open change: a queued change carrying the head of one waits
with `WAIT_UNQUEUED_BELOW`, since merging it would merge that change's commits unqueued.
So does one built on it before a merge of main went on top: planning fetches each
unqueued head, peels those merges off, and holds a change carrying what is left. A change
built on a fork waits on the fork's kick-back the same way; the fork's head is fetched
for its ancestry only, and only when the listing holds another change.

## Stacks

A change is stacked on another when it carries that change's head and main does not:
the other is open in the queue, or merged as a squash or a rebase, which leaves its head
off main. A merge of main into the change beneath (GitHub's "Update branch", or an
update commit the queue pushed) does not break the stack. The nearest such change is the
change's stack base, and its own delta is measured from there:

- it merges after the change beneath it, in the same partition whatever their keys;
- its candidate is built from its stack base, so a line it deleted from the change
  beneath stays deleted after that change merges as a squash, where the plain merge from
  its fork point would bring it back;
- when the change beneath is kicked back, or waits, or its candidate cannot be built,
  it waits too and is never blamed: `WAIT_BELOW_KICKED` or `WAIT_BELOW`.

The provider's `parent` must agree with ancestry. A change declaring a parent it is not
built on waits with `WAIT_RESTACK`; one declaring a parent the queue does not list waits
with `WAIT_BELOW`. Refused, with `KICK_REFUSED`:

- a stack mixing merge methods, since a squashed change beneath a merged one appears
  twice in `git log`;
- a stacked change with the rebase method, until predicting a rebase no longer needs git's
  experimental `replay`;
- a change built on two queued changes neither of which is built on the other;
- changes stacked on each other in a cycle, as two changes at one commit are;
- a stack more than 16 unmerged changes deep.

A change stacked on a squashed one usually merges through an update commit whose tree is
the validated one: the tip plus the change's own delta, which is the diff its reviewers
approved. Where the provider needs stacked branches linear, the update commit sits on the
tip alone: it replaces the branch's commits with one holding their delta, made by the
queue (never passed off as the author's), naming the head it replaces, whose commits stay
reachable by id. The queue refuses that replacement while another open change is stacked
on the head it would replace.

An approval carries over a rebase that only moved the change: its whole diff, replayed
onto the new base without a conflict, is exactly the new head. It does not carry over
when the approved commit carries an unqueued change's head, carries commits of a listed
or merged change without that change's current head, or forked from anything but its
stack base, since the approval was given on a diff that excluded what the rebase includes.

Where the provider merges stacks atomically, a validated run of changes stacked on each
other merges in one call through its top, when merging each one's own delta gives each
one's validated tree. The call pins every member to the head it was validated at. The
queue then checks every member's tree and shape, and stops when the provider merged only
part of the run.

## Hooks on each candidate

A hook is a command and its arguments, not a shell line. `--gate`, `--regenerate` and
`--facts` are read once, when the flags are, as sh words: quotes and backslashes group
and escape, and nothing else is shell. A variable, a command substitution, a pipe, `;`,
`&&`, a redirection, a glob, a comment or a leading `NAME=value` is refused with
[MGS3026](../reference/codes/sandbox/MGS3026.md); put such a line in a script and point
the flag at the script. The queue runs the command directly, in a process group of its
own, in the candidate's checkout, and appends its inputs as arguments, the way `magus
run <target> [project...]` takes projects:

| Hook                    | Arguments                                                              | Stdin                          |
| ----------------------- | ---------------------------------------------------------------------- | ------------------------------ |
| `validate --gate`       | the change's affected projects                                         | nothing                        |
| `validate --regenerate` | the change's affected projects                                         | the generated paths to rewrite |
| `apply --regenerate`    | the projects that generate those paths, proven to run no change's code | the generated paths to rewrite |
| `--facts`               | the fact asked for: `affected`, `outputs`, `generation` or `all`       | what that fact takes           |

A change whose affected set is no proof (unknown, or `unbounded_by` set) gets every
project instead, spelled as the build tool spells it: `/` for magus, and what the facts
command answers to `all` for another tool. A change that reaches no project has nothing
to gate and validates green. So `magus run ci --no-default-charms` runs as `magus run ci
--no-default-charms libs/parser apps/web`, a line a person can run as it stands.

The gate is green on exit 0, and anything else its processes do is red: another exit
status, a death by signal, or exit 75 (`EX_TEMPFAIL`, which magus returns when a lock or
the machine budget is busy) three times running. Its projects are the top change's, so
it runs only what that change adds. When a candidate whose every change beneath is
validated is red, the queue gates the commit it was built onto with the same command
and the same projects, once a run for every change that asks. Red there, the change waits
with `WAIT_BASE_RED`, keeps its place and is told nothing; green, it is kicked back with
`KICK_RED`. The gate's output lines on stderr are tagged with the commit and the change
(`[4b1c0e9a2f31 #482]`), or with `base` for a commit gated as it stands. Each candidate
is a checkout of its own (a git worktree) with a scratch directory of its own; point
every cache there with `--scratch-env`.

A generated-file conflict takes the change's side and `--regenerate` rewrites it, with
the generated paths on stdin; without a hook, a file either side deleted stays deleted.
The regeneration's writes to outputs and to files `generate`'s targets update in place
are committed; a file magus maintains is restored to the candidate's version and left out. A
regeneration that fails, or writes anything nothing declares it writes, kicks that change
back and the run goes on.

## What a review covers

Approval is checked at the commit a review of the head covers: the head itself, or,
walking back through merges of main into the change, the commit beneath each merge that
adds nothing a reviewer did not see. A merge adds nothing when its tree is the plain
merge of its parents, or, for a change stacked on one that has merged, their merge from
its stack base; while the change beneath is open, that second form would drop its
content, so it is not accepted. A merge that differs from the plain merge only in
generated files adds nothing only when main's regeneration, run by apply on the plain
merge, gives exactly the merge's tree, and only after the build tool proves the change
touches none of that regeneration's code; otherwise the change waits with
`WAIT_NOT_APPROVED` for an approval at its head.

## Verdicts: `mergequeue.verdict/v1`

`validate --verdicts <dir>` first writes the plan there as `plan.json`, then one
directory per decided change, named by its id, holding `verdict.json`. Each appears by
rename the moment its change is decided, so a reader never sees a partial one, and each
is checked when written as well as when read. `.done` beside them says the run finished,
and a full run writes it even when it stopped early. Validating a different plan into a
directory that already holds one is an error.

```json
{
  "schema": "mergequeue.verdict/v1",
  "base_commit": "9f2e...",
  "change": {"id": "483", "head": "c0de...", "base": "main", "method": "squash", "fork": false, "title": "feat: lexer"},
  "decision": "merge",
  "after": "482",
  "onto": "5e1a...",
  "candidate_commit": "77aa...",
  "method": "squash",
  "message": "* add lexer",
  "depth": 2,
  "duration_ms": 41230
}
```

`decision` is `merge`, `kick` (with a `report` for the author), `wait` (with a
`reason`), or `merged` for a change whose head is already on the base; every `kick` and
`wait` carries a `code`, and `paths` and `with` name the files at issue and the base
commits that touched them. `after` is the change validated beneath this one and `onto`
its candidate; `method` is the merge method it was validated under. `gate` and
`regenerate` are the hook lines validation ran, which planning's verdicts never carry. `apply` polls the
directory, and merges a change once its own verdict is green and `after` has merged. It
trusts a verdict only as far as the plan vouches for it: a head, an `after`, an `onto`
or a missing change beneath that the plan does not match merges nothing, and a verdict on
a change the plan did not admit is dropped.

### Codes

| Code                    | Means                                                                |
| ----------------------- | -------------------------------------------------------------------- |
| `WAIT_NOT_APPROVED`     | no approval at the commit a review of its head covers                |
| `WAIT_HEAD_MOVED`       | its head moved since it was listed or validated                      |
| `WAIT_BEHIND`           | a change validated beneath it did not merge, or it was not validated |
| `WAIT_CONFLICT_AHEAD`   | it conflicts with a change ahead of it, which merges first           |
| `WAIT_REVALIDATE`       | what it was validated on is no longer what it would merge onto       |
| `WAIT_BRANCH_MOVED`     | its branch moved or was deleted before an update commit was pushed   |
| `WAIT_PROVIDER_REFUSED` | the provider refused the merge                                       |
| `WAIT_BELOW`            | the change it is stacked on has not merged                           |
| `WAIT_BELOW_KICKED`     | the change it is stacked on was kicked back                          |
| `WAIT_RESTACK`          | it is not built on the head of the change it says it is stacked on   |
| `WAIT_RETARGET`         | it targets another branch than the queue's base                      |
| `WAIT_METHOD_CHANGED`   | its merge method changed since validation                            |
| `WAIT_WITHDRAWN`        | its merge intent was withdrawn since it was listed                   |
| `WAIT_UNQUEUED_BELOW`   | it carries the commits of an open change nobody queued               |
| `WAIT_NO_COMMITTER`     | it needs an update commit, and nothing names who commits it          |
| `WAIT_BASE_RED`         | the gate was red on its candidate and on what that was built onto    |
| `KICK_CONFLICT`         | a real conflict with the base in files that are not generated        |
| `KICK_RED`              | the gate was red on its candidate, green on what it was built onto   |
| `KICK_REFUSED`          | something the author has to fix that is neither                      |

## Applying as each candidate goes green

Validation runs the changes' code with a read-only token; apply holds the write token
and runs none of it. In this repository they are two GitHub Actions workflows, and
verdicts pass between them one change at a time:

1. `queue.yaml` (read-only) plans, then fans the plan out as a job matrix, one
   `validate --only <id>` job per change up to the depth of each partition. Each job
   uploads its verdict as an artifact the moment it finishes.
2. `queue-apply.yaml` starts when validation is requested (`workflow_run: requested`), from
   main's definition with a write-scoped Actions token, and downloads each verdict
   artifact as it appears, while validation is still running: `apply` with the run as its
   source merges each change whose predecessors have merged, and stops once the
   validation run completes. A first job waits only to see whether validation's plan job
   runs at all; on a pull request event with no merge intent it is skipped, and so is
   apply.

The apply job runs in the `magus-queue` environment, which holds the app's key when
there is one, so every apply run is listed under the repository's Deployments as a
deployment to `magus-queue`. That list is the queue's run history, not a release.

The apply token is the job's own unless the repository adds the queue's own GitHub App
(see [Setting it up on GitHub](#setting-it-up-on-github)), so by default no long-lived
secret exists. Most merges are made by GitHub's auto-merge on behalf of whoever enabled
it, and start main's CI, CD and the queue's next run like any merge. A merge the queue
makes itself with the Actions token starts no workflow, so after one the job dispatches
those runs itself; `apply` marks each `merged` event GitHub made with `by_provider`, and
the job reads that to decide. With the app's token the queue's own merges start those
runs themselves, and the workflow tells the job not to dispatch.

Before each merge, apply re-reads the change's approval, merge intent, base and merge
method from the provider, rebuilds its candidate, and predicts the tree main will carry:
the rebuilt candidate's changes since what it was built onto, merged onto main. A change
whose intent was withdrawn, or that was pointed at another branch after planning, is
skipped; one stacked on a change that just merged, and still targeting that change's
branch, is retargeted at main, since the provider merges a change into its own base. A
changed merge method waits for the next run. A file that both the candidate and something
merged since it was built changed, such as a root index two disjoint partitions both
regenerate, is a combination nobody validated: the change waits and is validated again on
the next run. Right before asking for the merge, apply reads intent, base and head once
more. A kick-back is carried out only on the head it was decided for; a head pushed since
waits for a decision of its own.

The queue's status, `merge-queue`, is main's only required check. It reads `pending`
while a change waits and `success` only on the commit apply is about to see merged: right
before it goes green, apply reads main again and confirms it is still the tip the merge
was predicted onto, and a change whose main moved waits for the next run instead. Then the
provider merges it (on GitHub, auto-merge does, as soon as the required check passes), or,
if the provider does not merge it on its own in time, apply asks it to. No credential
bypasses the status. A success apply cannot follow through, because the provider refused
the merge or applying stopped, goes back to `pending` before apply returns, and every run
starts by setting back to `pending` any success an earlier run left on an open change.

The queue validates the merge result, not the branch, so GitHub's "Require branches to
be up to date before merging" stays off: it would force every head onto main's tip, which
the queue's candidates already account for, and the queue cannot push an update to a
fork's branch. What that leaves is a push to main from outside the queue (an admin's
bypass) between apply's read of main and the merge. The merge then lands on a main nobody
validated; apply's check after the merge finds main does not carry the predicted tree and
stops, and the next run plans from the new tip.

Why not have validation post a `merge-queue` status per candidate and trigger apply on
the status event? Posting a status needs `statuses: write` in the job that runs
pull-request code, and branch protection requires exactly that status, so a pull
request could mark itself green. Statuses posted with the Actions token do not start
workflows either. A `workflow_run: completed` trigger fires once per run, so apply
would wait for the slowest candidate. Artifacts are readable through the API as soon as
they are uploaded, which is what lets the write side follow the read side change by
change without either one holding the other's rights.

When the provider's own merge of a head would not give the rebuilt tree, apply pushes an
update commit onto the change's branch with a lease on the validated head, so a branch
that moved or was deleted is left alone, and a branch that refuses the push kicks that
change back without stopping the run. An update commit that merges the base in or
regenerates keeps the author of the change's head and is committed by the committer; one
that restacks a branch is the committer's own. It may differ from the provider's merge only in generated
files main's regeneration wrote, or, for a change stacked on a squashed one, in exactly
what that change merged. Anything else is kicked back, and so is an update commit for a
branch another open change also merges from. After the merge, apply checks that
main carries the predicted tree in the shape the merge method gives (one new commit for a
squash, a merge commit whose second parent is what it handed over, a line of commits for
a rebase), and stops otherwise.

## Setting it up on GitHub

The queue runs on the job's own Actions token and needs no secret. That is the whole
setup for most repositories, and adding the queue's own GitHub App later is
configuration alone: the workflows are the same files either way.

`magus queue describe` reads how the repository is wired and prints the rest as `gh`
commands, each under a comment saying what it does. magus never runs them, and never
changes a setting itself. It reads over the network with your token, so pass one:

```sh
GITHUB_TOKEN=$(gh auth token) magus queue describe --provider github --base main
```

`-o json` prints the same as the `setup` of a `mergequeue.capabilities/v1` document: the
status the queue posts and who its credential posts it as, every check the base requires
and the integration each is pinned to, the repository settings the queue needs, and the
steps.

### Without a credential: three steps

1. Commit `.github/workflows/queue.yaml` and `.github/workflows/queue-apply.yaml` (this
   repository's are the reference). Both setups use them unchanged.
2. Run the commands `describe` prints: allow auto-merge, and a ruleset of its own that
   requires `merge-queue` from GitHub Actions (integration 15368) with "Require branches
   to be up to date before merging" off. Your other rulesets stay as they are. On a
   phone, the same is Settings > General > Pull Requests > "Allow auto-merge", and
   Settings > Rules > Rulesets > New branch ruleset: target the default branch, "Require
   status checks to pass", add `merge-queue` with GitHub Actions as its source.
3. Decide about the required checks `describe` lists as running on `pull_request`. A push
   the queue makes with the Actions token (an update commit or a regeneration) starts
   runs that wait for someone to approve them, so those checks go unreported on it. Take
   them out of the required set, as this repository did with `ci gate`, or accept that
   such a change waits until someone approves its runs, or add the app.

Require `merge-queue` only once the queue is on the default branch. Before that, nothing
posts it, and every merge waits on it.

### The queue's own GitHub App: five more steps

Add it when any of these holds:

- a second person has write access, since anyone with write access can post a status
  from GitHub Actions, and only a status pinned to an app nobody else holds cannot be
  forged;
- you want to keep required checks that run on `pull_request`, since the app's pushes
  start them like anyone's;
- a pull request touches `.github/workflows`, which the Actions token cannot merge;
- you want main's `push` workflows to start on every merge, with nothing dispatched and
  nothing run twice.

1. Open the registration link `describe` printed last and click "Create GitHub App". It
   is pre-filled: private, no webhook, and contents, pull requests, commit statuses,
   actions and workflows write.
2. On the app's page, generate a private key. A `.pem` downloads.
3. Install the app on this repository alone.
4. Run `describe` again with `--app <slug>`, and run the commands it prints: they create
   the `magus-queue` environment with its secrets released to the default branch only,
   set the `MAGUS_QUEUE_APP_CLIENT_ID` variable to the app's client id, store the key as
   the environment's `MAGUS_QUEUE_APP_PRIVATE_KEY` secret, and delete the download. On a
   phone, paste the key into Settings > Environments > magus-queue > Add secret.
5. Apply the ruleset change it prints, which pins `merge-queue` to the app's id: a
   `gh api` rewrite of the ruleset step 2 created, or a link for any other.

The next `queue-apply` run finds the variable and the secret. `setup-magus` mints a token
for this repository alone that expires when the job ends, and the queue merges, pushes,
commits and posts its status as the app's bot. The key lives in the environment, and a
pull request's run is evaluated against its merge ref, so no pull request's workflow can
read it.

`apply` refuses to start, with [MGS3019](../reference/codes/sandbox/MGS3019.md), when the
ruleset pins `merge-queue` to one integration and it holds another's token: GitHub would
count none of the statuses it posts. That happens when step 5 is skipped, or when the
secret goes missing and the job falls back to its own token.

## Providers

A provider is a Buzz script run on an embedded gopherbuzz VM. It exports these
functions, each taking one record:

| Function         | Receives                                                                                    | Returns                                                                          |
| ---------------- | ------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------- |
| `describe`       | `{base, remote_url, status_context, app, setup_steps}`                                      | `{stack_merge, linear_stacks, methods, queue_label?, committer?, setup?}`        |
| `list_changes`   | `{base, remote_url}`                                                                        | `{changes, merged, unqueued}`                                                    |
| `approval_at`    | the change plus `{commit}`                                                                  | `{approved, head, base, method, queued, shared_with, reason?, approved_commit?}` |
| `list_green`     | `{base, remote_url, context}`                                                               | `{changes: [{id, repo, head}]}`                                                  |
| `post_status`    | the change plus `{commit, context, state, description}`                                     | `true` when recorded                                                             |
| `retarget`       | the change plus `{base}`                                                                    | `true` once the change targets `base`                                            |
| `merge_change`   | the change plus `{commit, message, through}`                                                | `{merged, by_provider?, reason?}`                                                |
| `kick_back`      | the change plus `{commit, code, report, paths, with, candidate_commit, source, reproduce?}` | `true` when both the comment and the removal happened                            |
| `mark`           | the change plus `{mark}`: `queued`, `rejected`, or empty for none                           | `true` once the change shows that mark and no other                              |
| `flag`           | the change plus `{flag, on}`: `changes_generator`, and whether to show it                   | `true` once the change shows the flag exactly when `on`                          |
| `list_artifacts` | `{source}`                                                                                  | `{complete, artifacts: [{name, url}], headers?}`                                 |

All but `list_artifacts` are required, and a script missing one is refused when it
opens; `list_artifacts` is required of a provider `apply` follows through a run. Every
returned key without a `?` is required: a missing one is an error, never a zero value,
since a missing `fork` or `queued` read as false would admit what the provider meant to
refuse. `stack_merge` is `sequential` or `atomic`, `methods` the merge methods the
repository allows, and a change with any other method is refused. `queue_label` is the
prefix of the label that queues a change, followed by its method, for a provider whose
merge intent is a label; `magus queue describe` prints it for tools such as the pull
request advisor. `committer` is `{name, email}`, the identity the provider's automation
pushes as, which commits every update commit unless `--committer` overrides it.
`setup` is asked for with a `status_context`: `{status_context, credential: {id, name?},
required_checks: [{context, integration?, events?}], settings: [{name, value, want}],
app?, steps: [{title, command? or url?}]}`. `credential` is the integration the write
credential posts statuses as (the `app` named, else the provider's default),
`required_checks` what the base requires and the integration each is pinned to, and
`steps` only when `setup_steps` is true, since they cost reads a job's token may not be
allowed. A provider that returns no `setup` skips apply's credential check. `approval_at` must
name the change's current head, base and merge method, whether it still carries merge
intent (`queued`), and the other open changes whose head branch is its branch
(`shared_with`); `approved_commit` names an older commit its approvals stand at, which
the queue carries over only across a rebase that changed nothing. `list_green` names
every open change, whatever it targets, whose head carries the status `context` at
success. `through` lists the changes beneath a stack's top that merge in the same call,
lowest first, each with the commit it must still be at. `merge_change` sets
`by_provider` when the provider merged the change on its own rather than on this call;
left out, it reads as the call's merge. `kick_back`'s `source` is the validation run
apply followed, empty for a directory, and `reproduce`, `{gate, regenerate}`, is there
only when validation decided the kick: the hook lines that validate the change again.
`list_changes`' `unqueued` records carry `repo?` and `mark?`, the mark the change shows
now. Apply marks `queued` every change the plan admitted when it starts, clears that mark
from an unqueued change still showing it, marks `rejected` after a kick-back and clears
the mark after a merge. A failed `mark` is a notice and applying goes on: the status and
the comment are the record.

A flag is a property the change shows beside its mark, set and cleared on its own; a
queued change can carry any flag. `changes_generator` says the queue cannot regenerate
the change's generated files itself. Planning records on each change it admits the
generated files it touches whose regeneration the build tool cannot prove runs none of
the change's code (`--facts generation`, the proof apply needs before it regenerates);
apply flags every admitted change holding any when it starts and unflags the rest, and
leaves a change planning held as it is. A kick-back for that same reason flags the change
first, since apply may have needed files regenerated that the change does not touch, and
passes `kick_back` the flag as `flag` (empty otherwise) for its comment to name. A
failed `flag` is a notice, as a failed `mark` is.

Scripts see Buzz's standard library and a `mergequeue` module whose
`request(method, url: .., body: .., headers: ..)` returns `{status, body}`; a response
over 32 MiB is an error, never a shorter answer. The records a script receives hold
strings, bools, lists of strings, records and lists of records. `--provider github` is built in;
`--provider path/to/provider.buzz` loads any other. The GitHub provider reads
`GITHUB_TOKEN` or `MERGEQUEUE_TOKEN` for its reads and only `MERGEQUEUE_TOKEN` for its
writes, so the read-only job cannot write by accident. Its listings page to completion or
fail: a cut listing would read as fewer pull requests, fewer merged changes, or a stale
labeler.

Merge intent is GitHub's native auto-merge, and, for a stack, a `queue: <method>` label
on its top pull request applied by someone who holds write access. The labeler is read
from the pull request's timeline; a timeline too long to read, or a labeler whose
permission cannot be looked up, leaves that stack unqueued and nothing else. Kicking a
change back posts a new comment, never an edit of an old one: the report, a link to the
validation run, collapsed blocks for reproducing it (`gh run download` of the plan, then
`magus queue validate --only` with the recorded hook lines) and for the files, the
`gh pr merge <n> --auto --<method>` that queues it again (for a stack, the label on its
top), and one JSON line of its code and files. It then removes the intent where it
lives: its own auto-merge, its own label, and the label on its stack's top. Last it
minimizes as outdated its own earlier kick-back comments on the pull request; a failure
there is printed to the apply job's log and does not fail the kick-back. It shows state
and properties as three labels, each created with a description the first time a
repository needs it: `mark` swaps between `merge-queue: queued` and
`merge-queue: rejected`, and `flag` adds or removes `merge-queue: changes a generator`
for `changes_generator` beside either. A kick-back carrying that flag names the label in
its comment. A label GitHub refuses to create for any reason but that the repository
already has it is an error naming GitHub's reason. None of these
labels starts with `"queue: "`, so none reads as merge intent. Its `describe` reports
the label prefix `"queue: "` and the committer
`github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>`, the
identity a workflow's token pushes as; with the queue's app, `queue-apply.yaml` passes the
app's bot as `--committer`. Its setup reads the base's rulesets and classic branch
protection, the app with `--app`, and for the steps the checks and workflow runs on the
head of the most recently updated pull request from the repository, which is how it
tells which required checks run on `pull_request`. A read refused with 403 or 404 names
the permission it needs.

On GitHub, `merge_change` for a pull request with auto-merge on waits up to a minute for
GitHub to merge it once the queue's status went green, then merges it itself through the
API, pinned to the head. A merge GitHub made in the meantime reads as GitHub's unless
the Actions bot made it. A stack, queued by label, has no auto-merge, so the queue always
merges it itself. No merge needs a bypass actor. Its `describe` narrows `methods` to what
the repository settings and every active ruleset rule targeting the base branch both
allow, dropping `merge` when one of those rules requires a linear history, and errors
when nothing is left in common.

## The library

The queue's packages are usable from Go. Its contract lives in
`github.com/egladman/magus/libs/mergequeue/types`, a near-leaf package importing only the
standard library and magus's `types`: the documents (`Changes`, `Plan`, `Verdict`), the
codes, `Provider`, `ArtifactLister`, `Gate`, `BuildFacts`, `VerdictSource`, and the
version control each step gets. Its generated testify mocks are in
`libs/mergequeue/types/gen/mocks`; magus's `types/gen/mocks.MockVCSDriver` stands in for
the version control. The root package, `mergequeue`, holds the three steps (`Planner`,
`Validator`, `Applier`, each built with its required dependencies by its constructor and
run with `Run`), the pure decisions they share (stack detection, partitioning, approval
carry-over), the document codecs, the verdict directory, the artifact follower and the
command hooks.

| Interface        | What it answers                                                               | magus's implementation      |
| ---------------- | ----------------------------------------------------------------------------- | --------------------------- |
| `ReadVCS`        | revisions, trees, ranges, ancestry, tree merges; fetching (planning)          | magus's `types.VCSDriver`   |
| `BuildVCS`       | `ReadVCS` plus checkouts, merges in them and local commits (validation)       | magus's `types.VCSDriver`   |
| `PushVCS`        | `BuildVCS` plus a leased push (applying)                                      | magus's `types.VCSDriver`   |
| `BuildFacts`     | affected sets, all units, how paths are written, what regenerating runs       | `client.Workspace`          |
| `Provider`       | list, describe, approve, set statuses, retarget, merge, kick back, mark, flag | the `provider` Buzz scripts |
| `ArtifactLister` | the artifacts a validation run uploaded                                       | the `provider` Buzz scripts |

Every merge, check and push is composed in the queue from the capabilities' facts, so
which merge base a prediction takes, which conflicts are the author's and which
differences a review need not see are decided once, whatever the version control.
`CommandFacts` is the `BuildFacts` behind `--facts`, which gets the fact it is asked for
as its one argument. Asked for `all`, it prints `{"units": [unit]}`, how the build tool
names every unit. Asked for `outputs`, with the paths on stdin, the command prints `{"outputs": [path], "updated": [path], "maintained":
[path]}`: the paths a target writes whole, the ones a target rewrites in place, and the
ones the build tool rewrites itself on every run. A missing key names none, so a command
that prints only `outputs` declares no update and maintains nothing.

Where Go ends and Buzz begins is a rule, not a taste. Go holds what the invariants are
proven over and what needs the machine: admission, partitioning, candidate order, the
merge checks, version control, processes, files and concurrency. Buzz holds what talks
to a system outside the repository, the provider and the CI system, as a pure function of
that system's answers and the record it was handed: it supplies facts and performs
writes, and decides nothing. A fact the queue acts on is re-checked in Go before it is
trusted: a change record passes `Change.Check`, an approval must name a head, a base and
a merge method, a declared stack parent must match ancestry, a stale head is unproven. A
knob is a fact, not a script: what the schedule computes over comes from the build tool
and the provider; how it computes is fixed in Go.
