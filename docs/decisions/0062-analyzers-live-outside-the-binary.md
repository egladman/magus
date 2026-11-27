---
title: "ADR 0062: analyzers live outside the binary"
order: 62
description: Analyzers, their runner and the finding type leave magus's binary. They live in one library, libs/analyzer, that a workspace imports by path or pins by registry path, and guard rules take the same record in libs/guardrule. Records Eli's rulings R1 to R14 on the analyzer design, made 2026-11-26.
tags: [adr, decision, analyzers, lint, libraries, policy]
status: accepted
date: 2026-11-27
---

# ADR 0062: analyzers live outside the binary

## Context

magus shipped its own verdicts in the binary. `Established` in `types/knowledge.go:1998`
is the one the binary holds, and the precedent analyzer, the `magus/lint` module and every
committed path through `.magus/workspaces/<name>` sit beside it. A repository's policy about
what fails a change then lives in magus, where that repository cannot read or change it.
`docs/design/principles/measures-in-verdicts-out.md` says a measure never becomes a verdict in
core, and said nothing refused one: `docs/decisions` ended at 0060 and no page named
`libs/analyzer`.

The analyzer design (`analyzer-design.md`, rulings section) keeps the measures in magus and moves
the verdicts into one published library. It leaves a workspace two ways to get it: import by
workspace path inside this repository, or pin by registry path under `spells:`. Eli ruled on
fourteen open questions on 2026-11-26. This ADR records those rulings so they outlive the
design file, which is a working note.

## Decision

The rulings are the decision. Each row names the ruling it carries.

| behavior                                                                                                                                                                                              | enforcement                                              | State   |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------- | ------- |
| Analyzers, the runner and the finding type leave the binary. The binary keeps the measures, the guard seams, the Buzz runtime and the pinned import. No analyzer is embedded in magus.                | `hack/lint/core-reads-no-analyzer-policy.buzz` (planned) | planned |
| Analyzers, the runner and the reference analyzers live in one library, `libs/analyzer`, a nested Go module that also holds the Buzz sources. Its Go package and Buzz module are both `analyzer` (R5). | the library layout                                       | planned |
| Only analyzers that name nothing of this repository live in `libs/analyzer` (R1). The rest stay in `hack/lint` under the same record and are not published.                                           | `an-library-imports` (planned)                           | planned |
| A pinned library is declared under `spells:`, in the map a remote spell uses (R2).                                                                                                                    | `magus spell lock`                                       | planned |
| `magus doctor` shows unadmitted precedents through a check the root magusfile declares by name and script. The binary does not read `analyzers.yaml` for it (R3).                                     | the root magusfile check (planned)                       | planned |
| Guard rules take the record and the import route. They do not take the runner, baselines or exits. The directory moves to `libs/guardrule` now, with the same one-word naming (R4, R5).               | `libs/guardrule`                                         | planned |
| The library publishes at every magus release under the release tag, and from main on every merge under one moving tag, so a workspace on a development binary can lock a digest (R6).                 | `spell-publish` for `libs/analyzer` (planned)            | planned |
| A workspace's own Go pass wiring is generated under `.magus/` from one `analyzer\pass(..., from: dir)` line. It is the first generated Go file in the tree and lint alone uses it (R7).               | `magus` generation under `.magus/` (planned)             | planned |
| ADR 0028's links are stripped on disk and everything through them is removed. `knowledge.workspaces` stays for crossing reads until adoption data says otherwise (R8).                                | review; `magus agent adoption` data                      | planned |
| A reference write rule in `libs/guardrule`, wired by a workspace, stops an agent writing `analyzers.yaml`. Nothing in the binary names the file (R9).                                                 | the reference rule in `libs/guardrule` (planned)         | planned |
| A person records a baseline with `magus run lint:rw . -- --record <name>`. The runner never records one on a first run by itself (R10).                                                               | the runner (planned)                                     | planned |
| A reference analyzer that a workspace neither runs nor declines is a failing finding (R11).                                                                                                           | the runner (planned)                                     | planned |
| A precedent family whose replay over 40 pull requests raises nothing is admitted (R12).                                                                                                               | the precedent replay (planned)                           | planned |
| buzzed's `suitetests` moves into `libs/analyzer` (R13).                                                                                                                                               | `libs/analyzer` (planned)                                | planned |
| buzzed's primary-stays-on-main rule and its load refusal stay buzzed's call. This plan removes only the reason buzzed states for them (R14).                                                          | buzzed's own rule                                        | done    |

### Principles checked

- [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  the binary stops carrying its analyzers with no compatibility shim. The break is recorded in
  `changes/unreleased/analyzer-adr.md` and each removing change stays green on its own.
- [Measures in, verdicts out](../design/principles/measures-in-verdicts-out.md): this decision is
  that principle applied. The page's "Enforced by" list names the refusal the design added, and the
  refusal is not built yet.
- [Buzz modules live in their layer](../design/principles/buzz-modules-live-in-their-layer.md):
  `libs/analyzer` is a library layer that imports no workspace path outside itself. The page gains
  that layer in this change.
- [Delete before you add](../design/principles/delete-before-you-add.md): `Established`, the
  `magus/lint` module and the committed workspace paths go, and the library adds one entry point.
- [A file a person edits is YAML](../design/principles/a-file-a-person-edits-is-yaml.md): settings
  stay in each project's own `analyzers.yaml`, never in the binary or `magus.yaml`.
- [Sit next to the tools](../design/principles/sit-next-to-the-tools.md): a golangci pass runs the
  tool's own config and binary. The generated file under `.magus/` is run by hand.
- [No silent defaults](../design/judgment/no-silent-defaults.md): precedents are strict by default,
  an unadmitted row prints, and a reference analyzer nobody runs or declines fails by name (R11).
- The outer harness stays outside (pending). Rule R9 touches an agent integration, so this decision
  cites that principle. It is on the unmerged branch `feat/proofread-library-improvements-87c508`
  (commit 94344b71c) and is not yet in `docs/design/`. Link its page once it lands.

## Alternatives

Keep the analyzers in the binary. This is what ships today. It keeps one verdict in magus, and it
puts a repository's thresholds where that repository cannot change them. The design's evidence is
`Established` at `types/knowledge.go:1998` and the precedent analyzer. Reopen it only if a
repository cannot run a library analyzer at all, which the spell route rules out today.

Two artifacts, one general and one for magus (R1, option C). It doubles the publish, the pin and
the upgrade step for one repository's rules. Reopen it if a second consumer needs a different
pin from the first.

Publish all 55 Buzz rules and all 28 passes, including magus-only ones (R1, option B). The
magus-only ones would fail or do nothing in every other workspace. Reopen it if a rule that names
this repository proves general once its settings move out.

Rename the inline map to a top-level `imports:` (R2, option B). It is the truthful name, and no
consumer declares a remote spell today, so the break is cheap now. It also renames the verbs and
four diagnostic pages. Reopen it once a second kind of import needs the key.

A second key, `modules:`, beside `spells:` (R2, option C). Two keys for one map adds a reader
for each, and magus already reads `spells:` for remote artifacts.

Doctor reads the `precedent` key of `analyzers.yaml` itself (R3, option B). It is the smallest
change, but the binary then knows the library's file. Reopen it if the root check cannot report
what doctor needs.

Doctor prints every row past magus's shipped defaults and does not know what is admitted (R3,
option C). It leaves the admitted set unknown, which is the state the design is meant to end.

A full third engine for guard rules, with a runner, settings and baselines (R4, option C). It
makes the move a condition of a rewrite of 14,455 lines of guard policy, and the design rejects that.
Reopen it when a guard rule needs baselines.

A compiled guard rule for `analyzers.yaml` in every workspace (R9, option B). It puts the file's
name into the binary, the coupling the design removes. Reopen it if the reference rule cannot be
wired without it.

The runner records a baseline on first adoption (R10, option B). A first run that writes a
baseline by itself accepts whatever exists. Reopen it if adoption data shows a person never records one.

## Consequences

A workspace that wants a magus-only analyzer gets it from a library, pinned in `magus.lock`, not
from the binary. The binary gets smaller, and magus stops refusing verdicts it holds.

Editor integration loses the custom passes. gopls and a bare golangci see only the stock linters,
and the design accepts that cost (ruling 3). A bare `golangci-lint run` reads the committed
`.golangci.yml`, which names only stock linters.

A standalone workspace must pin the library to a release. R6's moving tag lets a development
binary pin main, and the design says a binary ahead of the last release may refuse a library
pinned at that release. Whether CI publishes the moving tag on every merge is settled by R6.

Every State row above is planned. This ADR records decisions and builds nothing, so each row is
proved by its artifact when its change lands.

## Open questions

The design's own "Not verified" section lists the claims it did not check against the tree. This
ADR does not carry those claims as facts. Settle each one in the change that depends on it.
