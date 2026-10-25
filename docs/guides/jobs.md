---
title: Jobs
description: Coordinate work by hand with magus job (one person across two worktrees, two teammates sharing a file, and CI recording whether merged work still passes), and what the job store tells you about who holds what, since when, and what done means.
tags:
  [
    jobs,
    magus job,
    worktrees,
    teams,
    ci,
    write paths,
    magus job fork,
    magus job exec,
    magus job exit,
    magus job wait,
    magus job apply,
  ]
---

# Jobs

A job is a written agreement about a piece of work: which files it changes, which
revision it starts from, and what has to be true before anyone calls it done. You
declare one with `magus job fork`, take it in the checkout where you edit with
`magus job exec`, hand back what happened with `magus job exit`, and have
`magus job wait` check the result against the agreement. `magus ls jobs` is the
board. Every step is a command you type; magus records and answers, and moves
nothing on its own. That keeps a
[human in the loop](../design/principles/human-in-the-loop.md) when the workers are
agents: the job is the agreement a person can read before anyone starts and
check before anyone calls it done.

This page follows three people at a terminal: one developer splitting a refactor,
two teammates who both need the same file, and a CI job checking merged work. The
[second page](jobs/other-work.md) splits work that is not a refactor: a dependency
upgrade, a translation, a release checklist.

## What the store gives you

- **Who holds what.** Each job names the files it may write. `magus describe job`
  lists the files other live jobs hold as off limits, and `magus ls jobs -o json`
  carries the checkout each job was taken in (`checkout_root`) and the login that
  declared it (`registered_by`). A job one checkout holds is not taken from another:
  `job exec` there is refused until the holder gives it up.
- **Since when.** The same record carries when the job was declared (`created`),
  taken (`registered`) and last moved (`updated`), in Unix seconds, and every file
  a job gave up, with the time and the sha256 the file had at that moment.
- **What done means.** A job names one check, a target to run, and may declare
  goals. `magus job wait` grades evidence rather than anybody's word: a passing run
  recorded no earlier than the second the job was declared, and the diff since the revision the job
  started from. When it says no, it exits 1 and names every rule that failed.

The store is kept per repository in your state directory
(`$XDG_STATE_HOME/magus/jobs/<repo>/jobs.jsonl`, one JSON object per line), so every
worktree and clone on one machine reads the same set. It does not travel between machines, which is why the CI example declares
its own.

Every transcript below is what the command printed, compared byte for byte by the
scripts in `cmd/magus/testdata/script/job_people_*.txtar`. Paths read `~/src` and
output refs read `out<hex>`, the two parts that change from run to run.

## One person, two worktrees

Ana's shop prices things in whole units, and she wants cents. Two halves can move
independently: `total.sh` in the pricing directory and `tax.sh` in checkout. Both are checked by one
target:

<!-- golden: job_people_solo.txtar shop/magusfile.buzz -->

```buzz
import "magus";
import "proc";

// test runs the shell checks in test/run.sh.
export fun test(ctx: magus\Context, args: [str]) > void !> any {
    ctx.readsFiles(["pricing/*.sh", "checkout/*.sh", "test/*.sh"]);
    proc\exec("sh", ["test/run.sh"]);
}
```

She declares a job per half, from her main checkout:

<!-- golden: job_people_solo.txtar fork.out -->

```console
$ magus job fork refactor/pricing --criteria 'totals are computed in cents' --write-paths pricing/total.sh --check 'test .'
forked refactor/pricing, declared, with 1 write path(s). Its holder reads the terms with `magus describe job refactor/pricing` and takes it with `magus job exec refactor/pricing`
model tier: standard (rank 1), default: "the declared default" at magus/modeltiers
referenced outside the write paths: files that use what the write paths define
  unknown, no symbol index answered; `magus graph build` builds one
```

```sh
magus job fork refactor/tax --criteria 'tax rounds half up' --write-paths checkout/tax.sh --check 'test .'
git worktree add ../shop-pricing
git worktree add ../shop-tax
```

The fork recorded the revision her checkout was on as each job's checkpoint. The
board:

<!-- golden: job_people_solo.txtar ls-declared.out -->

```console
$ magus ls jobs
JOB               HOLDER  STATE     MODEL     PATHS  PROOF     CHECK
refactor/pricing  -       declared  standard  1      alone     magus run test .
refactor/tax      -       declared  standard  1      disjoint  magus run test .

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
```

HOLDER names the worktree a job was taken in, `-` until somebody takes it; `server`
marks magus's own housekeeping. `declared` means nobody has taken it yet, and
`job exec` moves it to `running`. PROOF is what the fork could prove
about the write paths in that checkout: `alone`, `disjoint` or `overlapping`. The
`in flight` line is about open pull requests, which this repository never fetched.
Every job verb also prints a `console` line on stderr, which says where to watch a
job in a browser once `magus server start` is running.

In `../shop-pricing` she takes the first job:

<!-- golden: job_people_solo.txtar exec.out -->

```console
$ magus job exec refactor/pricing
recorded job refactor/pricing's base as 58d5e2c343b3312b97a2ac07d85607b3044e9b7d, which is the checkpoint it was handed. Nothing to reconcile; carry on.
took refactor/pricing in ~/src/shop-pricing; read the terms with `magus describe job refactor/pricing`
```

Had the worktree been on a different revision, exec would have said so and recorded
it anyway: a divergence is a fact for the record, not a refusal. The terms:

<!-- golden: job_people_solo.txtar describe.out -->

```console
$ magus describe job refactor/pricing
job: refactor/pricing
model tier: standard (rank 1), default: "the declared default" at magus/modeltiers
skills: short

criteria
totals are computed in cents

write paths
  pricing/total.sh

referenced outside the write paths: files that use what the write paths define
  unknown, no symbol index answered; `magus graph build` builds one

projects
  .

deny paths the workspace declares
  checkout/tax.sh: owned by live lease refactor/tax: coordinate, never work around
  magus.yaml: workspace configuration: changing it changes the plan every lease is running

grouping history
  none recorded for these projects

the only check you run
magus run test .
refactor/pricing: 0 of 1 goal(s) met
  [unmet] check
      carries no output_ref, so there is no run to reopen
net lines: +0 -0 (+0)
exports no goal declares: none
left defined with no use: none
```

The other half's file is listed as somebody else's, and the check is unmet because
no run has been filed. The last three lines measure her diff so far, which is empty:
they report, and never decide whether the job passes. She edits `total.sh` and runs the check:

```sh
magus run test .
# [pass] shop-pricing (ran, <duration>)
# magus run test .
# ref  out<hex>
```

The `ref` line names that run's captured output (see
[output references](../concepts/cache/output-refs.md)). Her result says what she
changed and which run proves it:

<!-- golden: job_people_solo.txtar result.in.json -->

```json
{"schema_version": 2,
 "changed_paths": ["pricing/total.sh"],
 "validation": {"command": "magus run test .", "output_ref": "out<hex>"},
 "unresolved_risks": []}
```

<!-- golden: job_people_solo.txtar exit.out -->

```console
$ magus job exit refactor/pricing --stdin < result.json
exited refactor/pricing, recorded exited, with its result filed. Whoever forked it verifies with `magus job wait refactor/pricing`
```

The tax half turns out not to be needed yet. She takes it in `../shop-tax` and
gives it back with no result:

<!-- golden: job_people_solo.txtar abandon.out -->

```console
$ magus job exit refactor/tax
ended refactor/tax, recorded no_return: abandoned by its holder, which filed no result
```

`no_return` is not a failure. A failed job came back and said so; an abandoned one
said nothing, and the board keeps the two apart. The end reason says which kind of
`no_return` it was: a holder that gave the job up, a read-only scout returned with
nothing to grade, a result nobody verified, or someone other than the holder ending it.

Back in the main checkout, she checks the result against the job:

<!-- golden: job_people_solo.txtar wait.out -->

```console
$ magus job wait refactor/pricing
verified refactor/pricing, recorded pass
its holder reports it ran magus run test .
goal check: verified (out<hex>)
footprint: where its diff since the checkpoint landed
  pricing/total.sh:1-1
```

`wait` reopened the run the ref names in the worktree that took the job, confirmed it
passed and was recorded after the job was declared, and read the diff since the
checkpoint: every changed line is inside the job's write paths. A run older than the
job, a failing run, or a change outside `total.sh` would have been named
here, and wait would have exited 1. Whether the change is good code is still Ana's
call; wait checks what is mechanical. Both jobs have ended, so `ls jobs` lists them only
with `--all`:

<!-- golden: job_people_solo.txtar ls-done.out -->

```console
$ magus ls jobs --all
JOB               HOLDER        STATE      MODEL     PATHS  PROOF     CHECK
refactor/pricing  shop-pricing  pass       standard  1      alone     magus run test .
refactor/tax      shop-tax      no_return  standard  1      disjoint  magus run test .

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
```

### Letting the fork make the worktree

Ana made both worktrees by hand, and removing them is hers to remember. `--checkout`
hands both ends to magus:

```sh
magus job fork refactor/pricing --criteria 'totals are computed in cents' --write-paths pricing/total.sh --check 'test .' --checkout ../shop-pricing
```

The fork makes the checkout at the job's checkpoint, or at the revision her checkout
is on when the job names none: a git worktree, an hg share or a jj workspace. Sapling
has no second checkout and refuses. The directory must lie outside the workspace. When
her checkout runs its own `./magus`, the fork copies that file in; when she runs magus
from PATH, it copies nothing and the job uses PATH too. The fork prints the checkout,
`describe job` shows it, and `job exec` takes the job there and nowhere else.

Once the job ends (pass, fail or no_return), the next `job wait`, `job exit` or
`job prune` removes the checkout if nothing in it is uncommitted and every commit in
it is on a branch, a bookmark, a remote or the revision it was made at. Otherwise it
keeps the checkout and records why, which `describe job` prints. A command run inside
the checkout never removes it.

## Two people, one file

Ana and Ben share a workstation, each in a worktree of the shop: `../ana` and
`../ben`. Ana takes a job on `total.sh` and `format.sh`, both in the pricing directory:

```sh
magus job fork cents --criteria 'prices are integers of cents' --write-paths pricing/total.sh,pricing/format.sh --check 'test .'
magus job exec cents
```

Ben's change needs `format.sh` too, and he declares it anyway:

<!-- golden: job_people_team.txtar fork.out -->

```console
$ magus job fork currency --criteria 'prices carry a currency code' --write-paths pricing/format.sh,checkout/tax.sh --check 'test .'
forked currency, declared, with 2 write path(s). Its holder reads the terms with `magus describe job currency` and takes it with `magus job exec currency`
model tier: standard (rank 1), default: "the declared default" at magus/modeltiers
referenced outside the write paths: files that use what the write paths define
  unknown, no symbol index answered; `magus graph build` builds one
```

The store accepted the claim and recorded the overlap. For a file with a diff driver,
such as Go source or Markdown with `diff=markdown` set, the same fork is refused
([MGS3032](../reference/codes/sandbox/MGS3032.md)); the
[translation example](jobs/other-work.md#translating-pages-without-sharing-one)
shows that refusal. A shell script has none, so the overlap is recorded rather than
refused.

Before he edits, Ben asks whether the file is free:

<!-- golden: job_people_team.txtar shell.out -->

```console
$ magus shell --path pricing/format.sh -o name
advise
```

An advisory, exit status 0. Without `-o name` the text says the path is inside
`cents`, quotes its criteria, and says this is not a block: a person who names no
job is never stopped from writing their own repository (the
[`leased-path`](../reference/rules/leased-path.md) rule). The question is also noted
on Ana's job, as an `unattributed` entry in `magus ls jobs -o json`, so she can see
somebody outside her job was about to write there.

Ben takes his job, and the board shows the shared ground:

<!-- golden: job_people_team.txtar ls-overlap.out -->

```console
$ magus ls jobs
JOB       HOLDER  STATE    MODEL     PATHS  PROOF  CHECK
cents     ana     running  standard  2      alone  magus run test .
currency  ben     running  standard  2      alone  magus run test .

overlaps
  cents and currency claim common ground
    cents: pricing/format.sh
    currency: pricing/format.sh
    footprints: disjoint

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
```

`footprints: disjoint` means neither of them has changed a line the other has. They
talk, and Ana hands the file over by applying her job's record without it. The
record is the whole spec, so it repeats what stays:

<!-- golden: job_people_team.txtar apply.out -->

```console
$ magus job apply -f - <<'EOF'
{"schema_version": 11, "id": "cents",
 "criteria": "prices are integers of cents",
 "write_paths": ["pricing/total.sh"],
 "check": {"target": "test", "project": "."}}
EOF
updated cents, still running:
  write_paths -pricing/format.sh
```

`magus job fork --schema` prints every field a record takes, and `--dry-run` prints
the change without writing it. The dropped path is recorded on `cents` as a release
carrying the file's sha256 at that moment, so Ben can tell later whether he built on
the file Ana left. The overlap is gone:

<!-- golden: job_people_team.txtar ls-handed.out -->

```console
$ magus ls jobs
JOB       HOLDER  STATE    MODEL     PATHS  PROOF  CHECK
cents     ana     running  standard  1      alone  magus run test .
currency  ben     running  standard  2      alone  magus run test .

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
```

and Ben's terms now list only `total.sh` as Ana's:

<!-- golden: job_people_team.txtar describe.out -->

```console
$ magus describe job currency
job: currency
model tier: standard (rank 1), default: "the declared default" at magus/modeltiers
skills: short

criteria
prices carry a currency code

write paths
  pricing/format.sh
  checkout/tax.sh

referenced outside the write paths: files that use what the write paths define
  unknown, no symbol index answered; `magus graph build` builds one

projects
  .

deny paths the workspace declares
  pricing/total.sh: owned by live lease cents: coordinate, never work around
  magus.yaml: workspace configuration: changing it changes the plan every lease is running

grouping history
  none recorded for these projects

the only check you run
magus run test .
currency: 0 of 1 goal(s) met
  [unmet] check
      carries no output_ref, so there is no run to reopen
net lines: +0 -0 (+0)
exports no goal declares: none
left defined with no use: none
```

### Splitting a file by declaration

Handing a file over is the fallback. When the two jobs touch different declarations of a
file with a diff driver, each row claims its own with `<file>#<declaration>` and neither
waits. `--depends-on` is for two jobs on the same declaration.

A fork refused with [MGS3032](../reference/codes/sandbox/MGS3032.md) prints the claim pair
to use. It reads the declarations the holder has touched so far and the symbols the new
job's goals name, and says whether the two are disjoint. It never writes a claim into
either row; the person or orchestrator who declares the rows re-forks them with the pair.

A claim names a declaration, and the word must name exactly one declaration of its file.
`ctx` or `String` names every line that holds it, so the fork is refused with
[MGS3031](../reference/codes/sandbox/MGS3031.md), which prints words that name one each.
A word that names none is accepted, because the declaration may not be written yet.
A key claim in a magusfile or `magus.yaml` names its key along the dots, so
`project.targets.release` names that key and not `project.targets.release-build`, and a
parent key such as `project.targets` claims every entry under it.

When removing a code path, flag or test reaches the goal, deleting it is a valid route:
give the deletion to the job that owns that declaration rather than adding a parallel path.

A job that already holds a glob may narrow it, and containment is judged by glob
match. A job holding `internal/job/*.go` may narrow to `internal/job/breadth.go`,
because every path the new entry names is one the glob matches. Going the other way,
from `internal/job/breadth.go` to `internal/job/*.go`, widens the claim: the fork checks
it as a new claim, with the breadth and overlap checks that a new claim gets.

## Checking a record before you fork it

`magus job fork` validates the whole record before it writes anything. A record with
problems is refused in full: every problem is listed at once, each with its code, the
field it names, what is wrong and the edit that clears it. Nothing is stored, so the
record can be corrected and sent again.

To check a record without forking it, add the global `--dry-run` flag:

```sh
magus job fork --stdin --dry-run < job.json
```

It prints the problems, the record as it would be stored, the problems a deviation
accepted, and each check that could not run. It exits 1 while any problem is left. A
missing workspace or knowledge graph is named among the checks that could not run,
never read as a clean record. Nothing is written, so run it as often as needed.

These are the codes a fork can raise. Each code page says what the problem means in
full.

| Code                                             | Problem                                                                                                                                  | Fix                                                                                                   |
| ------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| [MGS3037](../reference/codes/sandbox/MGS3037.md) | A field cannot be read as declared: an id, a timeout, a path outside the workspace, an unknown member                                    | Edit each field the problem names                                                                     |
| [MGS3038](../reference/codes/sandbox/MGS3038.md) | A goal no evidence could grade: a duplicate id, a dependency cycle, or a symbol the graph resolves to more than one declaration          | Name one symbol per goal, using the id `magus refs` prints for an ambiguous one                       |
| [MGS3039](../reference/codes/sandbox/MGS3039.md) | The job writes and declares no check or goal, or names no write paths without being `read_only`                                          | Add the check that proves the change, or set `read_only`                                              |
| [MGS3040](../reference/codes/sandbox/MGS3040.md) | A check the worker cannot run: an unknown target, a command the guard denies, or arguments that skip the tests of the packages it writes | Name a target the workspace has, with arguments that still run those tests                            |
| [MGS3041](../reference/codes/sandbox/MGS3041.md) | A checkpoint this checkout cannot place                                                                                                  | Fork where that revision is checked out, or name the checkpoint `magus vcs checkpoint -o name` prints |
| [MGS3042](../reference/codes/sandbox/MGS3042.md) | A fork past `jobs.max_depth` or `jobs.max_live`                                                                                          | Fork under a shallower parent, end a live job first, or change the limit in `magus.yaml`              |
| [MGS3043](../reference/codes/sandbox/MGS3043.md) | A glob write path too broad for `jobs.max_claim_files` or `jobs.max_claim_projects`                                                      | Name the files the job edits, or permit the path                                                      |
| [MGS3044](../reference/codes/sandbox/MGS3044.md) | A target regenerates an output from sources the job may edit, and the output is outside its write paths                                  | Add the output to the write paths, or permit it when the root regenerates it                          |
| [MGS3045](../reference/codes/sandbox/MGS3045.md) | A ci target reads files the job may write, and neither its check nor a goal runs it                                                      | Add a goal for the target, or permit it when the root runs it after integration                       |
| [MGS3046](../reference/codes/sandbox/MGS3046.md) | A workspace file (a magusfile, `magus.yaml` or a spell source) is in the write paths while another live job works in this checkout       | Fork the job with `--checkout` into its own worktree, or leave the file out of its write paths        |

The claim refusals from before this series keep their own pages:
[MGS3018](../reference/codes/sandbox/MGS3018.md) for a directory write path,
[MGS3031](../reference/codes/sandbox/MGS3031.md) and
[MGS3032](../reference/codes/sandbox/MGS3032.md) for claims, and
[MGS3036](../reference/codes/sandbox/MGS3036.md) for a whole-file configuration claim.

### Deviations

A record can permit three problems and no others: MGS3043, MGS3044 and MGS3045. A deviation
names the code, the subject the problem reports, and a reason:

```json
"deviations": [
  { "code": "MGS3043", "subject": "**/*.go", "reason": "renames the type everywhere" }
]
```

The subject is what the problem names: the write path for MGS3043, the output path for
MGS3044, and `target project` for MGS3045, such as `lint .`. A deviation matches the code
and the subject spelled exactly, so a deviation for `**/*.go` does not cover `**/*.md`. A
deviation on any other code is refused under MGS3037, and so is a deviation with no subject
or no reason.

Magus checks that the reason is not blank and keeps the deviation on the stored job. It
does not judge the reason. A reviewer reads it, so write it for that reader: say why
this job needs the whole path, or who runs the output or the gate instead. A permitted
problem does not refuse the fork, and `--dry-run` lists it apart from the problems left.

### Limits on a glob write path

MGS3043 measures glob write paths only. Literal paths, `file#declaration` claims and
read-only jobs are not measured. For a glob, the fork counts the files it matches, leaving
out the files VCS ignores, and counts the projects those files belong to. Two keys in
`magus.yaml` set the bounds:

- `jobs.max_claim_files`, default 50: the most files one glob may match.
- `jobs.max_claim_projects`, default 1: the most projects one glob may reach.

Zero or a negative number is an error. Both keys are read from the workspace's own
`magus.yaml`, not from your user configuration. If that file cannot be read, the fork
refuses the glob under MGS3043 and says the breadth was not judged, rather than
passing it. A glob that begins with `**` is refused whatever it matches.

The fix for an MGS3043 problem gives the narrowest write paths the job's symbol goals
and criteria name, as a ready `write_paths` array, then the deviation that keeps the glob.
When the knowledge graph cannot answer the symbol goals, the fix says so; `magus graph
build` and a fork again give a fuller list. MGS3042 reads `jobs.max_depth` and
`jobs.max_live` the same way, and a zero limit is unlimited.

### Files outside the write paths that use them

A fork that stores a writing job lists the files outside its write paths that reference
a symbol its write paths define. These are the tests and callers a change reaches one
hop out. `magus describe job` shows the same list. Tests come first, then the files with
the most references. Each line names the write paths the file uses, and the file itself
is the path to add when the change reaches it. A `file#declaration` claim counts only
the references to that declaration.

The list reports and never refuses. A file a target declares as an output is left out,
because MGS3044 reports it. With no symbol index to ask, the fork and `magus describe
job` say the list is unknown and name `magus graph build`, rather than printing an empty
list. `-o json` carries it as `referenced_outside`, and the reason it is unknown as
`referenced_outside_gap`.

## CI checks the merged work

Each job passed in the worktree it was written in. Whether the work still passes
once the branches are merged is a different question, and `magus job wait
--integration` answers it in the tree it runs in. A CI runner starts with an empty
store, so the plan is committed beside the code:

<!-- golden: job_people_ci.txtar shop/ci/jobs.jsonl -->

```json
{"schema_version": 11, "id": "release/pricing", "criteria": "totals are computed in cents", "write_paths": ["pricing/total.sh"], "check": {"target": "test", "project": "."}}
{"schema_version": 11, "id": "release/tax", "criteria": "tax rounds half up", "write_paths": ["checkout/tax.sh"], "check": {"target": "test", "project": "."}}
```

with the evidence the CI step fills in once it knows the run's ref:

<!-- golden: job_people_ci.txtar shop/ci/evidence.tmpl.json -->

```json
{"schema_version": 2,
 "changed_paths": [],
 "validation": {"command": "magus run test .", "output_ref": "out<hex>"},
 "unresolved_risks": []}
```

The step, on the merge commit:

```sh
magus job apply -f ci/jobs.jsonl
ref=$(magus run test . -o jsonl | sed -n 's/.*"ref":"\(out[0-9a-f]*\)".*/\1/p')
sed "s/out<hex>/$ref/" ci/evidence.tmpl.json > evidence.json
for job in release/pricing release/tax; do
  magus job wait "$job" --integration --stdin < evidence.json || exit 1
done
```

<!-- golden: job_people_ci.txtar apply.out -->

```console
$ magus job apply -f ci/jobs.jsonl
created release/pricing, declared, with 1 write path(s)
model tier: standard (rank 1), default: "the declared default" at magus/modeltiers
created release/tax, declared, with 1 write path(s)
model tier: standard (rank 1), default: "the declared default" at magus/modeltiers
```

<!-- golden: job_people_ci.txtar pass.out -->

```console
$ magus job wait release/pricing --integration --stdin < evidence.json
integration: release/pricing passes in ~/src/shop; its state is unchanged
release/pricing: 1 of 1 goal(s) met
  [met] check
```

The grade is recorded on the job as its `integration`, beside its state, which does
not move:

<!-- golden: job_people_ci.txtar ls.out -->

```console
$ magus ls jobs
JOB              HOLDER  STATE     MODEL     PATHS  PROOF     CHECK
release/pricing  -       declared  standard  1      alone     magus run test .
release/tax      -       declared  standard  1      disjoint  magus run test .

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
```

When the merge breaks the totals, the same step fails the build:

<!-- golden: job_people_ci.txtar fail.out -->

```console
$ magus job wait release/pricing --integration --stdin < evidence.json
integration: release/pricing fails in ~/src/shop; its state is unchanged
release/pricing: 0 of 1 goal(s) met
  [unmet] check
      the run behind output ref "out<hex>" failed, so its check did not pass
```

Exit 1 is a verdict: the evidence was read and the check did not pass. Exit 2 means
magus could not answer at all, such as evidence that would not decode. The runner's
store goes away with the runner, so keep the record with `-o json`, which prints the
grade as JSON, if you want it after the build. See [CI](integrations/ci.md) for
running magus in a pipeline.

## Where to go next

- [Splitting other work](jobs/other-work.md): a dependency upgrade, a translation
  and a release checklist, each split into jobs.
- [Model tiers](jobs/model-tiers.md): declare the tiers your jobs run at, map each
  to a harness's model, and optionally pick one with a function at fork.
- `magus job <verb> -h` ends with an example for that verb, and
  [`magus job`](../reference/manpage/magus-job.md) has every flag.
