---
title: "ADR 0023: touched functions keep change risk at 10 and kill their mutants"
order: 23
description: A function a change touches is refused when its change risk passes 10 or a mutant of it survives, and it is worse than the same function at base. Spells declare decision tokens and where their coverage profile lands as data, and one engine in core scores every language. Change risk has no exception. Coverage floors hold each changed file's covered lines against its base. Change risk is built and lint-rules runs its rule. Mutation moves to a go::mutate op with a Go-native engine and a token fallback, and is not built here.
tags: [adr, decision, lint, coverage, mutation, spells, gates]
status: accepted (partly implemented)
date: 2026-11-03
---

# ADR 0023: touched functions keep change risk at 10 and kill their mutants

## Context

Coverage is gated once per repository (`hack/magusfile/coverage.buzz`) and says nothing
about a function. That file names the failure it fears: tests written to defend a number,
which execute code and assert nothing. Agents produce them cheaply. Before this work magus
had no per-function complexity, no per-function coverage it trusted (the `@coverage` shard
credits a Go block to the nearest preceding definition), and no mutation testing.

What already existed and was reused:

- every symbol index records a body extent (`DefEndLine`) and a body digest;
- `magus diff` classifies each touched symbol as added, re-signed or body-changed and
  hangs checks on it;
- spells declare comment syntax as data that one generic engine consumes;
- `mgs_getSymbolIndexer` shows how a spell declares a capability by exporting it and
  gets a reserved op.

The first wave of the build was reviewed on 2026-11-03 (architecture review of
`65d6322fa..d3b69f1f7`, the branch that built it). The review found, among others, that
every spell's coverage command was broken because `$MAGUS_COVERAGE_DIR` sat inside a
token and the runner resolved whole tokens only, that a mutant's timeout came from a
baseline the action cache could replay, and that `Magus.Mutants` did not exist. Each fix
changed the shape the first design described, so the Decision below records what is on
the branch.

## Decision

Eli decided that the threshold is 10 and that only touched functions are refused: a
function the change does not edit is never charged.

### Score

`change risk = c^2 (1 - cov)^3 + c`, with c the function's complexity. The formula
is the CRAP score of Savoia and Evans (2007); magus calls it change risk.
It does not measure whether a test asserts anything: a line a test runs counts as
covered.

- **Complexity is decisions plus one** (`Complexity = decisions + 1`). A spell declares
  its decision tokens as data (`Syntax.Decisions`); the engine counts them inside the
  function's indexed span, outside comments and strings. The bash indexer counts from its
  AST instead, because bash has no honest token rule.
- **Complexity rides magus's pack beside SCIP.** `SymbolInfo.Complexity` is stored in the
  gob-encoded `symbols.Shard`, the pack magus owns, and the in-process reader applies it
  after parsing. It is not written into a SCIP `Documentation` string: the first build did
  that, and the line leaked into the interchange SCIP other tools read. `ExportSCIP`
  ignores the field.
- **Coverage is the profile the project's tests write.** `mgs_getCoverage` declares
  `Coverage{format, profile}`: an LCOV or Go coverprofile report at a path relative to the
  project. It synthesizes no op. The project target that declares the profile among its
  outputs writes it, so core names no target and no path of its own. cov is covered units
  over coverable units in the function's span.
- **A line is credited only by its own package's tests.** Without `-coverpkg`, `go test`
  instruments only the package being tested (`go help testflag`), so the Go profile the
  root `test` writes already holds that rule. `magus buzz -t --coverprofile` records only
  the module under test, so `buzz_test` joins one fragment per module.
- **Runner refs resolve inside a token only for `MAGUS` and `MAGUS_` names.** A command
  token such as `--output=$MAGUS_SYMBOL_INDEX.scip` expands `$MAGUS_SYMBOL_INDEX`. Any
  other `$NAME` inside a token is left to the shell, so a user's variable is not
  rewritten.

### Refuse

A function the change adds, re-signs or edits whose score passes 10 is a lint finding. A
function that cannot be measured is a finding too, unless c^2 + c <= 10 decides it at any
coverage. A function that only moved, with an unchanged body digest, is not touched.

### Mutate

This section is the decision for mutation, which moves to the go spell's `mutate` op
([2026-11-04: mutation moves to go::mutate](#2026-11-04-mutation-moves-to-gomutate)).
Nothing below is built in core.

Each touched function's covered, non-arid lines yield the spell's token swaps
(`Syntax.Mutations`). A swap is `Find` and `Replace`, because `from` is a reserved word in
Buzz.

- **The baseline is scoped like the mutants.** The coverage op runs once per scope in a
  scratch copy, unmutated, over the same scope the spell's test op then runs, narrowed by
  `Command.TestScope`. Lines that baseline did not hit are dropped: no scoped test can
  kill a mutant it never runs. Cross-package coverage is not credited, for the same
  reason.
- **Each mutant runs the spell's own test op** in the scratch copy with `--no-cache`. Exit
  zero survives. Any other exit, or a run past 10 times the baseline, kills.
- **The cache never decides a mutant.** A run whose result was replayed from the action
  cache ran no test, so the engine refuses it by name instead of counting it as a kill.
  The timeout therefore always comes from a baseline that ran.
- **Outcomes are kept** in `mutants.jsonl` (JSON Lines, last entry per key wins), keyed by
  the mutant's identity and the scoped test op's cache key, so a changed test or tool
  misses. Deleting the file changes a duration, never an answer.

### Except

`// magus:mutant op=<swap> reason=<why>` beside a site excepts that one surviving mutant.
It is the only exception form. A marker that matches no survivor, or carries no reason, is
a finding. Change risk has no exception form: a score past 10 is fixed by a test or a smaller
function.

### Where the threshold lives

The threshold is the repository's rule, not magus's. Core holds no language and no
number; it reports scores and mutants, and a lint rule decides.

### State

| item                                                                        | State                                                 |
| --------------------------------------------------------------------------- | ----------------------------------------------------- |
| decision tokens and the coverage profile declared by spells as data         | done                                                  |
| complexity carried in the pack beside SCIP (`SymbolInfo.Complexity`)        | done                                                  |
| runner refs resolve inside a token for `MAGUS` and `MAGUS_` names           | done                                                  |
| `Magus.ChangeRisk` and the change-risk check in `magus diff`                | done                                                  |
| Buzz methods `Change.risk` and `Workspace.risk`                             | done                                                  |
| lint rule `touched-functions-keep-change-risk-under-10`                     | done (tested; not yet listed in `hack/lint/run.buzz`) |
| wiring the rule into `hack/lint/run.buzz` and warming it in `lint_rules`    | planned (once main's CI publishes the base profile)   |
| head profile freshened through the cache; base profile kept per commit      | done                                                  |
| each touched function judged against itself at base                         | done                                                  |
| covered lines kept, in place of the 70% Go and 62% Buzz totals              | done                                                  |
| `Change.risk` scoring this repository's own change                          | in progress (waits on main's CI base profile)         |
| mutation: a go::mutate op, Go-native engine plus token fallback, one binary | not built (the next change)                           |
| the mutant verdict cache, keyed by body, covering tests and Context         | not built (the next change)                           |
| the `magus:mutant` marker and the kill-their-mutants rule                   | not built (the next change)                           |
| coverage for bash                                                           | not built                                             |
| Measured section (refusals and wall time over the last 20 merges)           | not built                                             |

On the building branch, change risk against `origin/main` ran the root `coverage` op for
12m47s and failed: that op ran the whole Go suite, which was red. Listing the rules under
`lint-rules` then deadlocked (MGS3007), because `Change.risk` started a run on the project
the outer run held. Mutation stopped within five minutes: its first Buzz subject swapped
the `>` of a return type for `>=`, and the docs magusfile no longer parsed.

### Judge against base

Eli decided on 2026-11-03 that removing code never costs. A touched function is charged
only when it is past 10 and its change risk is worse than the same function's at base, or
when it has more surviving mutants than it had at base. This is a measurement, not a
waiver: the base is scored the same way, over the merge base's source and its kept
coverage profile.

- **The base span comes from the patch.** The function's lines map back through the
  working tree's hunks and the commits' hunks. Removed lines inside the span belong to
  it, so a shrunk function is scored over what it was.
- **A function new since base, or one whose base cannot be measured, meets 10 alone.** The
  answer says which (`BaseReason`), so the fallback is never silent.
- **Only a function with a survivor is mutated at base.** None cannot be worse. Each
  mutant carries its function's survivor count at base.

### Coverage is read, not run

Superseded on 2026-11-04 ([coverage is the tests' own profile](#2026-11-04-coverage-is-the-tests-own-profile)). `Change.risk` first ran each
coverage op in a scratch copy of the working tree and again in an export of the merge
base, scoped to the touched files' tests. On this change that took 402 s at head, 274 s
of it cmd/magus, and stopped at its 7m budget before base began (outd3b4e003ef3d). ci
already ran the same tests in `test`, so the tree was tested twice.

Now, at head, core finds the project target that declares the spell's profile as an
output and freshens it the way `freshenSymbolIndexes` freshens an index: a step the cache
vouches for is read as it stands, and a stale one runs through the scheduler of the run
that holds the project, so nothing nests (MGS3007). `lint_rules` calls `Change.risk`
once before its child process reads the rules, as it already does for precedents. A
profile a run freshened is recorded under that run's root invocation, so the nested magus
reads it while its digest still matches; a record lives a day. A red test makes each
function of its project a gap that names the command.

At base, the profile is the cache entry main's CI wrote for the declaring target at the
merge-base commit. Core exports the base only to key the target by the base's own inputs,
against this workspace's store and its remote tier, and replays the entry there. It never
runs the base, and no base binary is built. No entry is an error that names the base
commit and the command, decided 2026-11-05: a base that is not measured would judge the
change against nothing. A read keeps the profile in the workspace store per commit.

### Covered lines kept

Eli replaced the whole-workspace coverage floors (70% of Go statements in `test`, 62% of
Buzz lines in `buzz_test`) on 2026-11-03: deleting well-covered code lowered a total. The
change-risk rule now reports, per changed file, a line covered at base that is still there
and no longer covered, and a coverable line the change added that no scoped test covers.
A removed line is never compared, and a file whose coverage could not be measured is a
finding that says why.

### Principles checked

- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  relied on; token tables are facts a spell declares and the engine is the one behavior.
- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on; a missing
  extent, a missing report, an empty report, a red baseline, a replayed run and a missing
  capability are each a finding or an error that names the cause.
- [Sit next to the tools](../design/principles/sit-next-to-the-tools.md): strained; magus
  counts decisions itself rather than running gocyclo, ESLint or ruff. No one tool spans
  the languages, Buzz and bash have none, and every test still runs through the spell's
  own op.
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md): the
  failure removed is a test that runs code without asserting on it.
- [Measured, not asserted](../design/principles/measured-not-asserted.md): pending; this
  record has no measurement of refusals or wall time yet, and claims none.

## Alternatives

- **A native tool per spell, parsed by a provider op.** Lost: five parsers and five
  installs, no tool for Buzz or bash, Python's tools are Python, most report no end line
  and the definitions of complexity differ. Reopen if the token tables disagree with a
  native tool on more than a measured share of functions; none is measured.
- **A conformance metric.** Lost: conformance judges file counts against the base from the
  index, while change risk needs a test run and is a property of one function.
- **A ledger or baseline of known survivors
  ([ADR 0021](0021-a-change-accepts-the-findings-it-causes.md)).** Lost: a survivor is a
  property of the tree and outlives the merge, and the tool-native suppression is the
  inline marker. Reopen if a survivor class appears that no marker can sit beside.
- **Mutating untouched functions, or the whole tree in CI.** Lost: it charges bystanders
  and takes hours. Reopen with a measured wall time that fits a gate.
- **No worse than the base alone, through the conformance ceiling.** Lost: Eli decided a
  touched function past 10 is refused. The base now decides only for a function already
  past 10 ([Judge against base](#judge-against-base)), and it is measured by the same
  engine, not by a conformance count.
- **A nested coverage run, or `ctx.needs` on the root `coverage` op.** Lost: the nested
  run deadlocked under `lint-rules`, and the root op runs the whole suite, so one red
  test anywhere made every function a gap.
- **Complexity in a SCIP `Documentation` line.** Built first and removed: it leaked into
  the interchange SCIP and needed a filter in the reader.
- **Resolving every `$NAME` inside a command token.** Lost: it rewrites a user's own
  variable. Only `MAGUS` and `MAGUS_` names are magus's to expand.

## Consequences

- Every touched function needs a test that asserts, except functions small enough that
  c^2 + c <= 10 decides them at any coverage.
- Lint runs no test of its own. It waits for the project's test target at head when ci
  has not run it yet, and for one run at a base no cache holds.
- Bash functions of complexity 3 or more are refused, because bash declares no coverage.
- A project whose tests do not write the profile its spell declares, as a declared
  output, gets a loud gap that names the missing declaration.

## Open questions

- Bash coverage needs a runner such as bats with kcov. Decide when a shell file with
  complexity 3 or more is edited.
- The 10 times baseline timeout can be tiny for a fast suite. No floor is configured; add
  one only with a measured false kill.

## Amendments

### 2026-11-04: the score is called change risk

Eli renamed the score to change risk. The Buzz members moved onto objects: `Change.risk()`
and `Change.mutants()` measure from the Change's base, and `Workspace.risk()` scores every
function. The first lint rule became `touched-functions-keep-change-risk-under-10`.

### 2026-11-04: judged against base, measured in copies

A touched function is judged against itself at base, coverage runs scoped in scratch
copies, and covered lines kept replace the coverage totals. See
[Judge against base](#judge-against-base), [Coverage is read, not run](#coverage-is-read-not-run) and [Covered lines kept](#covered-lines-kept).

### 2026-11-04: mutation moves to go::mutate

Eli decided that this change lands change risk alone. Mutation moves to a `mutate` op of
the go spell, built in the next change: a Go-native engine (go/ast sites, `go test
-overlay`) with the token swaps kept as its fallback, in the one magus binary. A mutant's
verdict is keyed by the function's body, the tests that cover it and the Context it runs
under. Core keeps no mutation code until then: `Magus.Mutants`, `Change.mutants()`,
`Syntax.Mutations`, the `magus:mutant` marker and `touched-functions-kill-their-mutants`
are removed, and [Mutate](#mutate) and [Except](#except) record the decision that op
builds.

### 2026-11-04: coverage is the tests' own profile

The scratch coverage runs stopped at their 7m budget inside `lint-rules` on this change
(outd3b4e003ef3d). Eli decided that change risk gets no target of its own and reads the
profile the project's tests already write: "The spell declares it." `mgs_getCoverage`
declares only `{format, profile}`, and the target declaring the profile as an output
writes it. The reserved `coverage` op, `Op.Coverage`, `MAGUS_COVERAGE_DIR` and the
measure's 7 minute budget are removed, since nothing runs coverage on change risk's
behalf. Eli kept the rule that a function is credited only by its own package's tests,
which the Go and Buzz profiles hold as written (see [Score](#score)). See [Coverage is read, not run](#coverage-is-read-not-run).

### 2026-11-05: the rule lands unwired

The base profile comes only from main's CI cache, and main's CI keyed the base of this
change before it changed the spell coverage contract, so the rule could only fail here.
Decided by the parent session, logged for Eli: the rule, `Change.risk` and their tests
land, and `hack/lint/run.buzz` does not list the rule yet. The follow-up is one line in
`hack/lint/run.buzz` and the warm in `lint_rules`, once main's CI has published the base
profile for a merge base that includes this change. The touched magusfile targets `test`
and `buzz_test` moved their logic into `hack/magusfile/coverage.buzz` and
`hack/magusfile/buzz-coverage.buzz`, which buzz-test measures, so they score under 3.
