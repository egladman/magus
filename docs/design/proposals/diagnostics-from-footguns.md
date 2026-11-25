---
title: Diagnostics from one session's footguns
description: Proposal, not built. Thirteen traps from the 2026-11-24 session, each checked against the code. Eight hold up and get a code or a rule with a fix line, five are dropped with the reason, and one more turned up running this page's own check.
tags: [diagnostics, codes, guard, jobs, drift, proposal]
---

# Diagnostics from one session's footguns

State: proposal, not built. Each entry stands alone, so one can be built without the others.

## The idea

On 2026-11-24 one long session hit thirteen traps. Each cost a person or an agent time
because magus said nothing, or said something that named the wrong cause.
[Stale is never silent](../judgment/stale-is-never-silent.md) asks for a code, a fix line,
and a second code for a second cause. [No silent defaults](../judgment/no-silent-defaults.md)
asks that a step magus skips says so. This page checks each trap against the code and keeps
the ones the code confirms.

Two kinds of answer come out, because magus has two places a refusal lives:

- An MGS code, for a condition the binary detects in any workspace. It needs a constant in
  `types/diagnostic.go`, a page under `docs/reference/codes/`, and the
  `diagnostic-codes-have-docs-pages` lint passes only with both.
- A rule of this workspace, in `hack/policy` or `hack/lint`. It carries a rule name and a
  fix line and no MGS code, because the condition is this repository's policy.

Free codes used below: MGS3059 and MGS4012. MGS4009 is retired
(`changes/unreleased/op-side-findings-the-drift-notice-no.md`) and is never reused.

## Kept

| #  | Trap                                          | Answer                              | Priority |
| -- | --------------------------------------------- | ----------------------------------- | -------- |
| 1  | Same cache key, different output              | new MGS4012                         | P1       |
| 6  | A store line from a build with other fields   | reuse MGS3047, stop using MGS3050   | P1       |
| 9  | A check rewrites a file outside the job       | widen MGS3044                       | P1       |
| 8  | A path held by a row nobody is working        | new MGS3059                         | P2       |
| 3  | A baseline row orphaned by a rename           | second message in the lint          | P2       |
| 4  | A pull request whose base CI never runs on    | new rule in `hack/policy`           | P2       |
| 12 | "ci" stays ambiguous after the person answers | patterns and a fix line in the rule | P2       |
| 11 | `job exec` on a compound line                 | one more sentence in the refusal    | P3       |
| 14 | A narrow run that judged no file passes       | fail in the check                   | P1       |

P1 is a wrong answer or a job that cannot finish. P2 is a refusal that is right and names
no way out. P3 is wording.

### 1. Same cache key, different output: MGS4012

Evidence. `types.ClassifyDrift` (types/vcs.go:1419) has three forks, and the two for
unchanged declared inputs are a dev build, MGS4005 "not your change, do not commit", and a
release build, MGS4003 "a non-deterministic generator". Its three callers all pass `false`
for the drift no declared source explains: run.go:3801, internal/doctor/checks.go:2821 and
internal/settle/ls_files.go:109. A generator that reads a file it never declared is in
neither fork, so on a dev build the reader is told to discard a real change. That is what
happened to the job bindings. [Stale is never silent](../judgment/stale-is-never-silent.md)
already lists the missing code as a debt.

Existing codes. MGS4003, MGS4005 and MGS4006 each name another cause. MGS4004 is a missing
project edge and MGS4010 a key read too early. None covers this.

Code. MGS4012, `UndeclaredInput`, in the determinism and drift family
(its page goes beside MGS4006's, under `docs/reference/codes/race/`).

Message and fix line.

```text
[MGS4012] docs types-generate wrote different bytes under cache key 887ccd21 than the entry stored
under it, and wrote the same bytes twice: it reads something its key does not hold.
differing: internal/job/gen/bindings.go
Declare it: add the file to the target's ctx.readsFiles, then `magus run types-generate:rw docs` and commit.
To find it: `magus explain docs:types-generate` lists what the key holds.
```

Where it fires. `runReplay` (run.go:3181), the `--race=replay` pass. Today it hashes the
outputs after the cache-aware pass, runs the handler once with the cache bypassed, and calls
any difference MGS4003.

How it tells the causes apart. Take whether the first pass replayed a stored entry for the
project. When it did and the fresh pass differs, run the handler a second time and hash again:

- fresh pass one equals fresh pass two: the generator is stable and the stored entry is
  wrong for this tree. MGS4012.
- they differ: MGS4003, as today.
- the first pass was itself a fresh run: MGS4003, as today. No third pass.

Then give `ClassifyDrift` the same fact. Add a parameter saying a stored entry exists under
the key the regeneration ran with. Entry present, inputs unchanged: MGS4012, on any build.
No entry: MGS4005 on a dev build and MGS4003 on a release, as today. A different toolchain
makes a different key, so skew still reads as skew.

Test. `types/vcs_test.go` has one case per fork of `ClassifyDrift`; add the fourth. In
`run_test.go`, a target whose handler copies an undeclared file into its output: store an
entry, change the file, run with `--race=replay`, expect MGS4012 and the differing path. A
handler that writes a counter keeps MGS4003.

Not covered. A plain `magus affected generate:rw` that hits the cache still replays the
stale entry without a word, because nothing re-executes. Only a re-execution can see it.
This workspace now refuses the cause ahead of time for its Go generators
(`hack/lint/generators-declare-what-they-read.buzz`).

### 6. A store line from a build with other fields: MGS3047, not MGS3050

Evidence. `UnknownMemberError` (libs/safetyscissors/job/record.go:931) says "unknown stored
fields ... (read the store with the writer's build)". Nothing gives it a code of its own.
The JSONL reader wraps it in `codec.LineError` (libs/codec/jsonl.go:455), and
`file.StoreDiagnostic` (internal/file/log.go:176) turns every `LineError` into MGS3050.
The [MGS3050 page](../../reference/codes/sandbox/MGS3050.md) says the line "holds nothing
any magus can read" and to delete it. Here the line is whole, a newer build wrote it, and
deleting it drops a live job row. The message names neither the binary that read the store
nor how to rebuild it.

Existing codes. [MGS3047](../../reference/codes/sandbox/MGS3047.md), "a record from a newer
magus", is this cause: the record is kept, and the remedy is the newer magus. Reuse it. No
new code.

Message and fix line.

```text
[MGS3047] ~/.local/state/magus/jobs.jsonl:41 holds fields this magus does not know
(spec.postconditions[0].accept), so a build with another job schema wrote it.
Read by /Users/me/Repos/magus/magus (v0.4.3-112-g742a976e3). The line is kept.
Relink it from the tree that wrote the line: `GOEXPERIMENT=jsonv2 go -C /Users/me/Repos/magus build -trimpath -o magus ./cmd/magus`
```

Where it fires. In `diagnose` (internal/job/store.go:330), in the chain that ends at line
370, before the fall through to `file.StoreDiagnostic`: match `*job.UnknownMemberError`
with `errors.AsType` and wrap it as `types.StoreRecordsNewer`. The binary path comes from
`os.Executable`, the version from `types.MagusVersionFromContext`, and the relink line from
`ward.RelinkAt`, which the contract refusal already uses
(internal/guard/load_contract.go:181).

How it tells the causes apart. A line that is not JSON, or is cut short, never reaches the
schema check and stays MGS3050. A line that decodes and carries a field the schema lacks is
MGS3047. A person's typo in a field name gets MGS3047 too; the message names the field
path, so the typo is visible, and no predicate separates the two.

Test. `TestStoreReportsAnUnreadableFile` (internal/job/store_test.go:1065) pins MGS3050 for
`{not json`. Add its sibling: a line with one unknown field under `spec` is
`types.StoreRecordsNewer`, is not `types.StoreLineUndecodable`, and the message holds the
field path and the binary path. Add the case to the MGS3047 page and a See also line on the
MGS3050 page.

Folds in trap 7, dropped below.

### 9. A check rewrites a file outside the job: widen MGS3044

Evidence. `docs:design-index-generate` reads `docs/design/**/*.md` and declares `docs/design.md`
as an in-place update (`ctx.writesFiles(..., editsExistingSource: true)`, docs/magusfile.buzz).
`magus describe file docs/design.md` prints it as `declared: update`. `docs:lint` needs
`format` and `conventions`, which reach it. The job that wrote this page holds one write
path, `docs/design/proposals/diagnostics-from-footguns.md`, a source of that target. Its
`magus describe job` lists five outputs "regenerated outside the write paths" and
`docs/design.md` is not among them. So the fork passed, the check rewrites `docs/design.md`,
and the guard refuses the write. The session saw the same with `docs/glossary.md`, which
`glossary-index-generate` declares as an update.

Existing codes. [MGS3044](../../reference/codes/sandbox/MGS3044.md), "a regenerated output
outside the write paths", is this condition. `RegeneratedOutside`
(internal/job/regenerated.go:61) walks each target's `TargetOutputs`, and its list leaves out
the in-place update above. Widen it to every file a target declares it writes. No new code.

Message and fix line. The MGS3044 text, with the kind named:

```text
[MGS3044] job diagnostics-proposal writes docs/design/proposals/x.md, a source of docs:design-index-generate,
which rewrites docs/design.md in place, outside the job's write paths.
Add docs/design.md to the write paths, or keep it out with a deviation in the record:
code MGS3044, subject docs/design.md, reason "the integrator regenerates it".
```

Where it fires. At `magus job fork` and `magus job apply`, where MGS3044 fires today, and in
the "regenerated outside the write paths" list of `magus describe job`.

How it tells the causes apart. An output and an in-place update get the same code, because
the fix is the same. The message says "rewrites in place" for an update, so the reader knows
the file is tracked source and its diff shows in review.

Test. `internal/job/validate_test.go` has the MGS3044 cases (lines 127 to 498). Add one: a
project with a target that reads a directory of pages and declares an in-place update of
an index file beside it, and a row writing one page only. Expect one
`OutputOutsideWritePaths` problem whose subject is the index file.

Reproduced while writing this page. The job's own check, `magus run lint docs`, failed with
"format -> generate -> glossary-index-generate: fs write docs/glossary.md: refused by the
guard [lease-write]" and the same for `design-index-generate` and `docs/design.md`. The
guard's refusal is right and says to ask the orchestrator. The fork should have said it first.

### 8. A path held by a row nobody is working: MGS3059

Evidence. `leasedBoundary` (internal/job/evidence/boundary.go:152) gives every path of
another editing lease one reason: "owned by live lease X: coordinate, never work around".
It never reads how long ago the row was touched. `magus doctor` does:
`checkJobTree` (internal/doctor/lease.go:35) calls `jobtypes.NewList(rows).Flag(now,
limits.StaleAfter)` and reports rows past `jobs.stale_after`. So the worker that meets the
refusal is told to coordinate with a holder that left hours ago, and only a person who
thinks to run doctor learns otherwise. About seventy `887-*` rows did this.

Existing codes. MGS3046 is a load file in a shared checkout. MGS3047 to MGS3058 are store
and budget conditions. None names a stale holder.

Code. MGS3059, `LeaseHolderQuiet`, workspace scope (its page goes beside MGS3058's, under
`docs/reference/codes/sandbox/`).

Message and fix line.

```text
[MGS3059] hack/lint/run.buzz is held by job 887-lint-run, which nobody has touched for 5h12m
(jobs.stale_after is 1h). Its holder may be gone.
A worker cannot end another job. The root or a person checks it: `magus job prune --dry-run --all 887-lint-run`
```

Where it fires. In `leasedBoundary`, as the `Reason` of the `TermsBoundary`, so
`magus describe job` and the guard's write refusal both print it. Pass `now` and
`StaleAfter` in; `checkJobTree` shows how both are read.

How it tells the causes apart. A row touched within `jobs.stale_after` keeps today's
sentence and no code: its holder is working, and coordinating is right. A row past it gets
MGS3059. The path stays refused in both: quiet is not proof of abandoned, and `job prune`
already refuses a row with uncommitted work.

Test. `internal/job/evidence/boundary_test.go:55` calls `leasedBoundary`. Two rows on one
path, one updated now and one updated two windows ago: the first reason is the old sentence,
the second starts with `[MGS3059]` and names the prune line.

### 3. A baseline row orphaned by a rename

Evidence. `compare` in `hack/lint/code-comments-follow-proofread-rules.buzz` (line 102)
reports a row whose file no longer exists as "the proofread baseline allows N findings in
heapattr.go, which is gone", with the hint "A person lowers the count to 0". After a
`git mv` the findings moved with the file, so `heap_attr.go` also reports each one as past
a count of 0. Two findings, one cause, and the hint fits a deletion only.

Existing codes. None apply: this is a workspace lint, and its findings carry a message and
a hint (`lint-findings-carry-a-message-and-a-fix`).

Rule. Same rule, second message.

```text
the proofread baseline row heapattr.go names a file that moved to heap_attr.go
hint: Rename the row's key to heap_attr.go in libs/x/analyzers.yaml; its counts stay as they are.
```

Where it fires. In `compare`, in the loop over `b.counts`, on the "is gone" branch.

How it tells the causes apart. A row is a move when its file is gone and exactly one file
with no row holds, per rule, the counts the row allows. Then report the move once and drop
that file's past-the-count findings. Any other gone row keeps "which is gone". The check
reads only what `compare` already holds, so a narrow run decides the same way.

Test. Beside `"a count above what the file holds, or a gone file, fails at its row"`: a
baseline row for `old.go` with `banner: 2`, findings of two banners in `new.go`,
`exists("old.go")` false. Expect one finding, the move, at the row's line.

### 4. A pull request whose base CI never runs on

Evidence. `.github/workflows/ci.yaml` triggers on `pull_request` with `branches: [main]`.
A pull request opened against another pull request's branch gets no run of that workflow,
and GitHub shows no pending check, so it reads as nothing to wait for.

Existing codes. None, and none should exist: the trigger is this repository's workflow.
`hack/policy/pr_workflow.buzz` already reads a workflow file as YAML to hold it to the guard.

Rule. `pr-base-has-no-ci`, in `hack/policy/pull_requests.buzz`, which already parses
`--base` and `-B` (line 202).

```text
pull request base `splice-typed-bindings` gets no CI: .github/workflows/ci.yaml runs on pull requests into main only.
Open it against main once the base merges, or add the branch to `on.pull_request.branches`. To open it anyway as a draft: `gh pr create --draft --base splice-typed-bindings`
```

Where it fires. On `gh pr create` and `gh pr edit` with a base. Deny when the base matches
no pattern in `on.pull_request.branches`; pass a draft, which nobody merges on green.

How it tells the causes apart. A workflow with no `branches` filter runs on every base, so
the rule passes. A file that does not parse fails closed and says the file could not be read.

Test. Buzz tests in the same file, in the style of `hack/policy/gh.buzz`: base `main`
passes, base `topic` is denied naming the workflow path, base `topic` with `--draft`
passes, and a workflow with no filter passes.

### 12. "ci" stays ambiguous after the person answers

Evidence, in internal/guard/ambiguous_ci_word.go. `ciMeansHost` lists `github\s+actions?`
and no `github\s+ci`. The message "GitHub CI" holds the bare word, matches neither pattern
list, and `recordAmbiguousCIWord` (line 77) arms the rule again. Its one caller is
`recordMessageTopics` (internal/guard/architecture.go:76), which reads typed messages only,
so an answer given through the host's question tool never reaches it. The denial says "Ask
which one, then retry" and does not say which words clear it.

Existing codes. None. It is a guard rule and keeps its name, `ambiguous-ci-word`.

Message and fix line.

```text
"ci" is ambiguous here: the magus `ci` target, or the host's CI/CD.
The person clears it by typing `magus ci` or `GitHub Actions` in a message; an answer in a question dialog does not count.
```

Where it fires. `denyAmbiguousCIWord` (line 99), and the pattern `ciMeansHost` (line 41).

Change. Add `(?:github|gitlab|hosted|remote)\s+ci` to `ciMeansHost`. Put the clearing
phrases in the denial. Reading a question tool's answer needs the harness descriptor to
name that tool, which is a larger change and is not proposed here.

Test. internal/guard/ambiguous_ci_word_test.go: "check GitHub CI" resolves and does not
arm; "check ci" arms; the denial text contains both clearing phrases.

### 11. `job exec` on a compound line

Evidence. The claim was that `orchestrator-groundwork` names no way forward. It names two:
`groundworkFinding` (internal/guard/root.go:198) says "fork a job for it ... and spawn a
worker on it", or, when spawning is broken, "take a row with `./magus job exec <row>`".
That half is dropped. The other half holds. A hook judges a whole line before any of it
runs, so `./magus job exec row && <groundwork>` is judged while the session still holds no
row, and the refusal does not say the line would pass in two parts.

Existing codes. None; the rule keeps its name.

Message. One sentence, added when the line being judged also calls `magus job exec`:

```text
This line takes a row and uses it at once, and a line is judged before it runs: run `./magus job exec <row>` alone on its line, then the rest.
```

Where it fires. `groundworkFinding`, with the predicate beside `isMagusInvocation` in the
shell walk the guard already does (`assumesCIWord` shows the walk).

Test. internal/guard/guard_test.go, beside the `orchestrator-groundwork` cases: a compound
line with `job exec` first gets the sentence, a line without it does not.

### 14. A narrow run that judged no file passes

Not one of the thirteen. Found running this page's check.

Evidence. `magus run lint docs -- docs/design/proposals/diagnostics-from-footguns.md` printed
"conventions: 0 files checked, no violations" and exited 0. The same run with the path
relative to the project, the same name without its leading `docs/`, found three violations. `conventionsCheck` (docs/engine/gates.buzz:670) filters its pages with
`pagesIn(pages, files: files)`, and a name that matches no page leaves nothing to check. The
passing run has an output ref, which a job files as proof.

Existing codes. None; it is this workspace's check. The root's `lintFiles` has the same
shape: it drops every argument for which `fs\exists` is false and returns ok when none is left.

Rule. In `conventionsCheck`, fail when `files` is not empty and one of them matched no page.

```text
conventions: docs/design/proposals/x.md names no page under docs, so nothing was checked.
Paths are relative to the project: `magus run lint docs -- design/proposals/x.md`
```

How it tells the causes apart. A path that exists under the workspace root with the project
prefix gets the line above, with the prefix cut. A path that exists nowhere gets "names no
file". No arguments keeps today's whole-tree run.

Test. A Buzz test beside `conventionsCheck`: one page, called with that page's
workspace-relative name, returns an error that holds the project-relative name. Priority P1:
it is a pass that proves nothing.

## Dropped

**2. Local lint passes while CI fails.** Not confirmed, so nothing here could be built.
Of its three parts: the "likely diagnostics" excerpt is already told
(internal/cache/cache.go:2426 prints the count of omitted lines and the full log's path,
and ci.yaml uploads that log); lint widening when `.golangci.yml` changes is the declared
read working (magusfile.buzz:3231); and the first part, a local whole-tree run with no
findings, has no cause I could show from the code. One suspect is worth a test.
golangci-lint keeps its own cache under `.magus/golangci-lint/<digest of analyzers.yaml>`
(`cacheDir`, magusfiles/quality/run-golangci.buzz:109), which magus does not key, and
`filenames` already carries a digest in its analyzer name because that cache keys a
package's findings on the analyzer name alone. `closedswitch` and `testpair` read across
packages and carry no such digest. To confirm: run `magus run lint .`, remove
`.magus/golangci-lint`, run it again, and compare. If the findings differ, that is trap 1
again in a tool's private cache, and it earns an entry then.

**5. Merged while checks were running.** Confirmed as an event: pull request 887 merged at
22:18:26 UTC on 2026-11-23, and `ci gate` failed at 22:59:14. Dropped as a diagnostic. The
head carried no `merge-queue` status, both rulesets on main let a repository role bypass
`always`, and the merge was a person's click on GitHub. No magus command ran, so nothing
could refuse. The fix is the bypass mode in the ruleset. `hack/policy/gh.buzz` already
denies an agent's `--admin` and `--auto` merges.

**7. Contract mismatch across checkouts.** Already a refusal that meets the bar.
`denyContractSkew` (internal/guard/load_contract.go:157) names the binary and its version,
both contract digests, the tree, and the relink line, and `authorityRecovery`
(internal/guard/stale_binary.go:104) is a second message for the second cause. What the
session lacked was the same care on the store's side, which is entry 6.

**10. `job exit` cannot record an expected local-only failure.** A feature request, not a
missing diagnostic. `refuseOwedReason` (internal/job/lifecycle.go:734) names the two reasons
it accepts and then four routes for the reasons workers reach for, among them "your
orchestrator drops a postcondition that is not this job's or adds a deviation". Accepting a
failing check as passing is the thing the refusal exists to stop.

**13. `filenames` false positives.** The finding already names its own exit. The message
(libs/conventions/filenames/analyzer.go:31) ends "if models is one established word, add it
to filenames' allow setting and say why". "models" as "mode" plus "ls" is a false positive
of the split at line 156, and the cure is a better split, not a code. "heapattr" was a true
positive: the rule asked for `heap_attr.go`.

## Checked against

- [Stale is never silent](../judgment/stale-is-never-silent.md): entries 1, 6, 8 and 3 each
  split one message that covered two causes with two fixes. Entry 1 pays the debt that page
  names.
- [No silent defaults](../judgment/no-silent-defaults.md): entry 9 makes a fork refuse what
  the check would later be unable to do. Entry 4 says a check is absent where an empty list
  read as nothing owed.
- [A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md): every message
  above ends in a line the reader runs.
- [Delete before you add](../principles/delete-before-you-add.md): two new codes. Entries 6
  and 9 reuse a code; five entries change a message in a rule that exists.
- [Refusals](../refusals.md): no row refuses any of these.
- magus computes and refuses, and it does not act. Entry 8 names the prune command and ends
  no job. Entry 5 is dropped because magus was not in the path.

## Order

1. Entries 9 and 14 first. Entry 9 blocks jobs today and is one function; entry 14 is one
   branch.
2. Entry 6: one branch in `diagnose` and two page edits.
3. Entry 1: the only one that adds a pass to a run. Build the `runReplay` half, then the
   `ClassifyDrift` parameter.
4. Entries 8, 3, 4, 12 and 11 in any order.
