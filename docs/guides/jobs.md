---
title: Jobs
description: Coordinate work by hand with magus job - one person across two worktrees, two teammates sharing a file, and CI recording whether merged work still passes - and what the job store tells you about who holds what, since when, and what done means.
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
declare one with `magus job fork`, take it in the checkout you will edit with
`magus job exec`, hand back what happened with `magus job exit`, and have
`magus job wait` check the result against the agreement. `magus ls jobs` is the
board. Every step is a command you type; magus records and answers, and moves
nothing on its own.

This page follows three people at a terminal: one developer splitting a refactor,
two teammates who both need the same file, and a CI job checking merged work. The
[second page](jobs/other-work.md) splits work that is not a refactor: a dependency
upgrade, a translation, a release checklist.

## What the store gives you

- **Who holds what.** Each job names the files it may write. `magus describe job`
  lists the files other live jobs hold as off limits, and `magus ls jobs -o json`
  carries the checkout each job was taken in (`checkout_root`) and the login that
  took it (`registered_by`).
- **Since when.** The same record carries when the job was declared (`created`),
  taken (`registered`) and last moved (`updated`), in Unix seconds, and every file
  a job gave up, with the time and the sha256 the file had at that moment.
- **What done means.** A job names one check, a target to run, and may declare
  goals. `magus job wait` grades evidence rather than anybody's word: a passing run
  recorded after the job was declared, and the diff since the revision the job
  started from. When it says no, it exits 1 and names every rule that failed.

The store is kept per repository in your state directory
(`$XDG_STATE_HOME/magus/jobs/`), so every worktree and clone on one machine reads the
same set. It does not travel between machines, which is why the CI example declares
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
    ctx.readsFiles("pricing/*.sh", "checkout/*.sh", "test/*.sh");
    proc\exec("sh", ["test/run.sh"]);
}
```

She declares a job per half, from her main checkout:

<!-- golden: job_people_solo.txtar fork.out -->

```console
$ magus job fork refactor/pricing --criteria 'totals are computed in cents' --write-paths pricing/total.sh --check 'test .'
forked refactor/pricing, declared, with 1 write path(s). Its holder reads the terms with `magus describe job refactor/pricing` and takes it with `magus job exec refactor/pricing`
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
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
JOB               HOLDER   STATE     MODEL  PATHS  PROOF  CHECK
refactor/pricing  session  declared  -      1      alone  magus run test .
refactor/tax      session  declared  -      1      alone  magus run test .

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

HOLDER reads `session` for every job a person declares; `server` marks magus's own
housekeeping. `declared` means nobody has filed anything yet. PROOF is what the fork could prove
about the write paths in that checkout: `alone`, `disjoint` or `overlapping`. The
`in flight` line is about open pull requests, which this repository never fetched,
and the `console` line says where to watch a job in a browser once
`magus server start` is running.

In `../shop-pricing` she takes the first job:

<!-- golden: job_people_solo.txtar exec.out -->

```console
$ magus job exec refactor/pricing
recorded job refactor/pricing's base as a1d513296908525d76f16e0f80c672c5ec69acbb, which is the checkpoint it was handed. Nothing to reconcile; carry on.
took refactor/pricing in ~/src/shop-pricing; read the terms with `magus describe job refactor/pricing`
```

Had the worktree been on a different revision, exec would have said so and recorded
it anyway: a divergence is a fact for the record, not a refusal. The terms:

<!-- golden: job_people_solo.txtar describe.out -->

```console
$ magus describe job refactor/pricing
job: refactor/pricing

criteria
totals are computed in cents

write paths
  pricing/total.sh

projects
  .

deny paths the workspace declares
  checkout/tax.sh: owned by live lease refactor/tax: coordinate, never work around
  magus.yaml: workspace configuration: changing it changes the plan every lease is running

the only check you run
magus run test .
refactor/pricing: 0 of 1 goal(s) met
  [unmet] check
      carries no output_ref, so there is no run to reopen
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

The other half's file is listed as somebody else's, and the check is unmet because
no run has been filed. She edits `total.sh` and runs the check:

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
abandoned refactor/tax, recorded no_return
```

`no_return` is not a failure. A failed job came back and said so; an abandoned one
said nothing, and the board keeps the two apart.

Back in the main checkout, she checks the result against the job:

<!-- golden: job_people_solo.txtar wait.out -->

```console
$ magus job wait refactor/pricing
verified refactor/pricing, recorded pass
its holder reports it ran magus run test .
goal check: verified (out<hex>)
footprint: where its diff since the checkpoint landed
  pricing/total.sh:1-1
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

`wait` reopened the run the ref names in the worktree that took the job, confirmed it
passed and was recorded after the job was declared, and read the diff since the
checkpoint: every changed line is inside the job's write paths. A run older than the
job, a failing run, or a change outside `total.sh` would have been named
here, and wait would have exited 1. Whether the change is good code is still Ana's
call; wait checks what is mechanical.

<!-- golden: job_people_solo.txtar ls-done.out -->

```console
$ magus ls jobs
JOB               HOLDER   STATE      MODEL  PATHS  PROOF  CHECK
refactor/pricing  session  pass       -      1      alone  magus run test .
refactor/tax      session  no_return  -      1      alone  magus run test .

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

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
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
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
JOB       HOLDER   STATE     MODEL  PATHS  PROOF  CHECK
cents     session  declared  -      2      alone  magus run test .
currency  session  declared  -      2      alone  magus run test .

overlaps
  cents and currency claim common ground
    cents: pricing/format.sh
    currency: pricing/format.sh
    footprints: disjoint

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
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
updated cents, still declared:
  write_paths -pricing/format.sh
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

`magus job fork --schema` prints every field a record takes, and `--dry-run` prints
the change without writing it. The dropped path is recorded on `cents` as a release
carrying the file's sha256 at that moment, so Ben can tell later whether he built on
the file Ana left. The overlap is gone:

<!-- golden: job_people_team.txtar ls-handed.out -->

```console
$ magus ls jobs
JOB       HOLDER   STATE     MODEL  PATHS  PROOF  CHECK
cents     session  declared  -      1      alone  magus run test .
currency  session  declared  -      2      alone  magus run test .

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

and Ben's terms now list only `total.sh` as Ana's:

<!-- golden: job_people_team.txtar describe.out -->

```console
$ magus describe job currency
job: currency

criteria
prices carry a currency code

write paths
  pricing/format.sh
  checkout/tax.sh

projects
  .

deny paths the workspace declares
  pricing/total.sh: owned by live lease cents: coordinate, never work around
  magus.yaml: workspace configuration: changing it changes the plan every lease is running

the only check you run
magus run test .
currency: 0 of 1 goal(s) met
  [unmet] check
      carries no output_ref, so there is no run to reopen
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
```

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
created release/tax, declared, with 1 write path(s)
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
JOB              HOLDER   STATE     MODEL  PATHS  PROOF  CHECK
release/pricing  session  declared  -      1      alone  magus run test .
release/tax      session  declared  -      1      alone  magus run test .

in flight: never fetched; `magus queue ls --provider <provider> --base <branch>` reads the open changes
console: nothing is serving it; `magus server start` to watch this job without interrupting its holder
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
- `magus job <verb> -h` ends with an example for that verb, and
  [`magus job`](../reference/manpage/magus-job.md) has every flag.
