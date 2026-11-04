---
title: "ADR 0023: touched functions meet CRAP 10 and kill their mutants"
order: 23
description: A function a change touches is refused when its CRAP score passes 10 or a mutant of it survives. Spells declare decision tokens, token swaps and a coverage command as data, and one engine in core scores and mutates every language. An equivalent mutant is excepted inline with a reason. CRAP has no exception. The engine, the spell declarations, the host functions and the two lint rules are built; the lint runner does not name the rules yet.
tags: [adr, decision, lint, coverage, mutation, spells, gates]
status: accepted (partly implemented)
date: 2026-11-03
---

# ADR 0023: touched functions meet CRAP 10 and kill their mutants

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
`65d6322fa..d3b69f1f7` on branch `crap-mutation`). The review found, among others, that
every spell's coverage command was broken because `$MAGUS_COVERAGE_DIR` sat inside a
token and the runner resolved whole tokens only, that a mutant's timeout came from a
baseline the action cache could replay, and that `Magus.Mutants` did not exist. Each fix
changed the shape the first design described, so the Decision below records what is on
the branch.

## Decision

Eli decided that the threshold is 10 and that only touched functions are refused: a
function the change does not edit is never charged.

### Score

`CRAP = c^2 (1 - cov)^3 + c`, with c the function's complexity.

- **Complexity is decisions plus one** (`Complexity = decisions + 1`). A spell declares
  its decision tokens as data (`Syntax.Decisions`); the engine counts them inside the
  function's indexed span, outside comments and strings. The bash indexer counts from its
  AST instead, because bash has no honest token rule.
- **Complexity rides magus's pack beside SCIP.** `SymbolInfo.Complexity` is stored in the
  gob-encoded `symbols.Shard`, the pack magus owns, and the in-process reader applies it
  after parsing. It is not written into a SCIP `Documentation` string: the first build did
  that, and the line leaked into the interchange SCIP other tools read. `ExportSCIP`
  ignores the field.
- **Coverage is declared on the op.** `mgs_getCoverage` makes magus synthesize an op of
  kind `coverage`, and the declaration is `Op.Coverage` (`spells/op.go`). The coverage
  command writes an LCOV or Go coverprofile report into `$MAGUS_COVERAGE_DIR`, which is
  that op's declared output. cov is covered units over coverable units in the function's
  span.
- **Runner refs resolve inside a token only for `MAGUS` and `MAGUS_` names.** A command
  token such as `--cov-report=lcov:$MAGUS_COVERAGE_DIR/lcov.info` (the python spell's
  pytest-cov evidence) expands `$MAGUS_COVERAGE_DIR`. Any other `$NAME` inside a token is
  left to the shell, so a user's variable is not rewritten.

### Refuse

A function the change adds, re-signs or edits whose score passes 10 is a lint finding. A
function that cannot be measured is a finding too, unless c^2 + c <= 10 decides it at any
coverage. A function that only moved, with an unchanged body digest, is not touched.

### Mutate

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
a finding. CRAP has no exception form: a score past 10 is fixed by a test or a smaller
function.

### Where the threshold lives

The threshold is the repository's rule, not magus's. Core holds no language and no
number; it reports scores and mutants, and a lint rule decides.

### State

| item                                                                          | State                                           |
| ----------------------------------------------------------------------------- | ----------------------------------------------- |
| decision tokens, token swaps and coverage declared by spells as data          | done                                            |
| `Op.Coverage` and the synthesized `coverage` op                               | done                                            |
| complexity carried in the pack beside SCIP (`SymbolInfo.Complexity`)          | done                                            |
| runner refs resolve inside a token for `MAGUS` and `MAGUS_` names             | done                                            |
| `Magus.Crap` and the crap check on each touched function in `magus diff`      | done                                            |
| `Magus.Mutants`, the scoped baseline, `mutants.jsonl` and the marker reader   | done                                            |
| the `magus:mutant` marker syntax and its unused-marker findings               | done                                            |
| Buzz host functions the two rules call (`magus\crap`, `magus\mutants`)        | done                                            |
| lint rule `touched-functions-stay-within-crap-10`                             | done (`hack/lint/run.buzz` does not name it)    |
| lint rule `touched-functions-kill-their-mutants`                              | done (`hack/lint/run.buzz` does not name it)    |
| `magus\crap` scoring this repository's own change                             | in progress (the root `coverage` op is red)     |
| `magus\mutants` over this repository's own change                             | in progress (a Buzz mutant breaks its parse)    |
| coverage for bash                                                             | not built                                       |
| Measured section (refusals and wall time over the last 20 merges)             | not built                                       |

Until `hack/lint/run.buzz` names the two rules, nothing refuses a touched function:
`magus diff` prints the crap check as advice and `Magus.Mutants` answers only when called.
On this branch `magus\crap` against `origin/main` ran the root `coverage` op for 12m47s
and failed, because that op runs the whole Go suite, which is red, and its buzz spell
calls `magus buzz -t` with no file. `magus\mutants` stopped within five minutes: its first
Buzz subject swapped the `>` of a return type for `>=`, the docs magusfile no longer
parsed, and that one unparsable mutant ended the whole run.

### Principles checked

- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  relied on; token tables are facts a spell declares and the engine is the one behavior.
- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on; a missing
  extent, a missing report, an empty report, a red baseline, a replayed run and a missing
  capability are each a finding or an error that names the cause.
- [A cache is an accelerator](../design/principles/a-cache-is-an-accelerator.md): relied
  on; deleting `mutants.jsonl` changes a duration, and a cache hit never decides a mutant.
- [Everything is a file](../design/principles/everything-is-a-file.md): relied on; one
  JSON Lines file.
- [Sit next to the tools](../design/principles/sit-next-to-the-tools.md): strained; magus
  counts decisions and mutates itself rather than running gocyclo, ESLint, ruff, Stryker or
  cargo-mutants. No one tool spans the languages, Buzz and bash have none, and every test
  still runs through the spell's own op.
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
  index, while CRAP needs a test run and is a property of one function.
- **A ledger or baseline of known survivors
  ([ADR 0021](0021-a-change-accepts-the-findings-it-causes.md)).** Lost: a survivor is a
  property of the tree and outlives the merge, and the tool-native suppression is the
  inline marker. Reopen if a survivor class appears that no marker can sit beside.
- **Mutating untouched functions, or the whole tree in CI.** Lost: it charges bystanders
  and takes hours. Reopen with a measured wall time that fits a gate.
- **No worse than the base, through the conformance ceiling.** Lost: Eli decided a touched
  function past 10 is refused.
- **Complexity in a SCIP `Documentation` line.** Built first and removed: it leaked into
  the interchange SCIP and needed a filter in the reader.
- **Resolving every `$NAME` inside a command token.** Lost: it rewrites a user's own
  variable. Only `MAGUS` and `MAGUS_` names are magus's to expand.

## Consequences

- Every touched function needs a test that asserts, except functions small enough that
  c^2 + c <= 10 decides them at any coverage.
- Lint gets slower by the scoped test runs of touched code when cold; a repeat run reads
  `mutants.jsonl`.
- Some survivors are equivalent mutants; each costs a marker with a reason.
- Bash functions of complexity 3 or more are refused, because bash declares no coverage.
- A node:test TypeScript project gets a loud gap that names vitest's coverage package,
  since the TypeScript spell declares vitest.

## Open questions

- Whether a second coverage declaration per spell is worth it for TypeScript projects on
  node:test. Not decided; revisit when such a project is touched.
- Bash coverage needs a runner such as bats with kcov. Decide when a shell file with
  complexity 3 or more is edited.
- The 10 times baseline timeout can be tiny for a fast suite. No floor is configured; add
  one only with a measured false kill.

## Amendments
