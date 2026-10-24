---
title: "ADR 0013: the guard denies"
order: 13
description: ADR 0006 section 2 shipped every built-in rule that guards a recoverable action as advice, said once a session. Six months of agent sessions show advice that only prints is routed around, so this record reverses it. Every built-in rule whose verdict names a remedy the caller can take now denies by default, shipped defaults included. A rule with no reachable remedy becomes a notice, which states a fact and never refuses. A workspace still lowers any deny to advise or off by name.
tags: [adr, decision, guard, agents, defaults, harness]
status: accepted
date: 2026-10-21
supersedes: "ADR 0006, section 2: the guard denies only what cannot be undone"
---

# ADR 0013: the guard denies

## Context

[ADR 0006](0006-the-person-drives-review.md#2-the-guard-denies-only-what-cannot-be-undone)
section 2 (#550) set every built-in rule that refuses a recoverable action, such as a
whole read, a pipe, an in-place edit, staging everything or a raw tool, to advise by
default, once a session. This repository set them back to deny in
`hack/policy/builtins.buzz`, with the evidence beside each one. The binary shipped advice.

Eli reversed that on 2026-10-21: an agent disregards advice that only prints, repeatedly,
and the work does not get done. The evidence, from the two sessions that ran on 10-20 and
10-21 (1,470 hook denials and 233 subagent transcripts, mined by two read-only scouts) and
entries 1 to 61 of the harness running document:

| #   | Gap                                                                                                                          | Class          | Cost                                      |
| --- | ---------------------------------------------------------------------------------------------------------------------------- | -------------- | ----------------------------------------- |
| G2  | Scripted writes not judged by effect: 30+ `python3 - <<EOF` rewrites over 20+ files passed where the same pattern was denied | sidestep       | silent                                    |
| G6  | A deny whose remedy the actor cannot reach: a stale-index remedy refused MGS3009, a skill demanded of a role without Skill   | dead-end       | ~30 denials, several agents ended blocked |
| G7  | False positives that teach routing around: 9 of 28 Read denials passed on an identical retry                                 | false-positive | trust in the guard                        |
| G11 | The gate gets skipped: two pull requests pushed ungated, since nothing tied a push to a passing gate for the pushed commit   | fail-open      | two PRs pushed ungated                    |
| G12 | Remedies that stop one step short: a deny promised `refs` would answer, and `refs` then refused the ambiguous name           | dead-end, loop | ~10 calls                                 |

Measured while the rules advised, in this repository's own policy file:

- raw-tool was an advisory first and changed behavior zero times over a long session,
  while uninstrumented runs left the Go build cache poisoned.
- spawn-unbriefed: across 2,147 session transcripts, zero loads of the multi-agent skill,
  while the two skills a hook demands loaded 415 times.
- architecture-unbriefed: 2026-10-10, 11 sessions met the advisory that names
  magus-architecture-review, and organic loads of it stayed near zero.

The rows split two ways. Advice that only prints does not change behavior (raw-tool,
spawn-unbriefed, architecture-unbriefed, G11). A deny whose remedy the actor cannot take
is a dead end that the actor routes around, and teaches it to (G6, G7, G12). A deny fixes
the first; only a reachable remedy fixes the second.

## Decision

### Every rule with a reachable remedy denies by default

| Item                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                          | State |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----- |
| The 28 recoverable rules deny by default: architecture-unbriefed, brief-command, brief-plain-commands, buzz-unbriefed, chained-run, exit-status-echo, filter-without-input, grep-reader, interpreter-rewrite, magus-timeout, monitor-duplicate, monitor-pile, output-pipe, output-redirect, raw-tool, read-navigation, read-paging, scripted-rewrite, search-translation, sed-in-place, sibling-checkout, spawn-unbriefed, spawn-waiter, stage-all, symbol-search, throwaway-copy, unjudged-tool, unknown-env | done  |
| The advisories whose text names a command deny by default: capture-filter, check-timeout, checkpoint-state, dependency-install, dependency-update, generated-write, graph-pipe, installed-skill, lease-invalid, model-tier-context-cap, spawn-nested-waiter; gate-repeat, a deny rule that advised by default, refuses the proven repeat again                                                                                                                                                                | done  |
| `builtin.defaults` in internal/guard/builtin/builtin.go holds the decisions, and `TestDefaults` and `TestRecoverableRulesDenyUnlessSetToAdvise` pin them                                                                                                                                                                                                                                                                                                                                                      | done  |
| A workspace lowers any of these by name in `magus\guard.builtins`, to advise or off; each test that pinned the old advice now sets advise and tests that opt-out                                                                                                                                                                                                                                                                                                                                              | done  |
| `hack/policy/builtins.buzz` keeps only read-navigation's 120-line limit, which the binary ships no number for, and the evidence for every rule it used to set                                                                                                                                                                                                                                                                                                                                                 | done  |

The cost rules (root-context-budget, root-heavy-command, root-result-cap, root-repeat,
spawn-names-model, spawn-above-model-tier) and stall are unchanged: [ADR 0009](0009-cost-profiles-for-agent-sessions.md) sets them by level, off until a level
turns them on.

### A rule with no reachable remedy is a notice

A notice states a fact and never refuses. `magus\guard.builtins` takes off or advise for
one and refuses deny as a misdeclaration that names why (MGS1045). `builtin.notices`
holds each notice with that reason, and `builtin.Verdicts` lists advise alone for it.

| Notice            | Why it never refuses                                                                                | State |
| ----------------- | --------------------------------------------------------------------------------------------------- | ----- |
| compat-marker     | whether code stays for an old caller is the root's call, and refusing the write would not ask it    | done  |
| echo-on-success   | the command it rides on already ran, and the echo changes nothing                                   | done  |
| focus             | focus-read and lease-write refuse what crosses the boundary; this states where it is                | done  |
| graph-stale       | it says why a graph-backed deny stood down                                                          | done  |
| hook-wiring       | it speaks only to a person's own session, and the check it names runs after the edit                | done  |
| instruction-write | only the author can tell whether a sentence restates a rule the tool already enforces               | done  |
| lease-state       | a diverged base or re-entered path is the row's state, which the write does not change              | done  |
| lease-terminal    | only the orchestrator reopens a finished row, so refusing its holder would leave it no call to make | done  |
| leased-path       | magus cannot tell the lease's own unnamed writer from a stranger                                    | done  |
| new-file          | a name that breaks a settled pattern may be the new pattern                                         | done  |
| new-source-dir    | a new directory is a boundary only its author can draw                                              | done  |
| precedent-search  | it fires with no symbol index too, where the refs it names answers nothing                          | done  |
| push-gate         | it fires on a gated push too; push-ungated refuses the push no green gate covers                    | done  |
| read-symbol       | the bounded read is the fallback read-navigation and read-paging name when the index is stale       | done  |
| regen-source      | the generate run it names comes after the edit it fires on                                          | done  |
| release-requested | it tells a holder about another fork's request, which the holder's own call does not answer         | done  |
| revert-classify   | classifying first changes nothing it reads, so a refusal would refuse every revert                  | done  |
| scope-drift       | a missing dependency edge may be the edge this change adds                                          | done  |
| shared-checkout   | it reads neither the new worker's isolation nor a disjointness proof                                | done  |
| skill-source      | it does not read whether the skill it names was loaded                                              | done  |
| source-read       | read-navigation refuses a whole read past its line limit; below it a whole read is the cheap answer | done  |
| spawn-unplaced    | the caller may be no worker at all                                                                  | done  |
| split-run         | both runs already ran by the time the second is judged                                              | done  |
| stage-classify    | stage-all refuses the unclassified stage; this states which files are generated                     | done  |
| timed-magus       | the timed run is the same run, and its durations are already in the log                             | done  |
| unleased-write    | magus cannot attribute the write, so there is nobody to name a remedy to                            | done  |

The four fixed rules (guard-unfinished and the three workspace-rule-failed notices) were
notices already: a workspace cannot set them at all. No rule was deleted: every notice
above is read by the agent it fires on, and each has a site in internal/guard.

The decision diverges from the 2026-10-21 plan for push-gate, hook-wiring,
lease-terminal, precedent-search, read-symbol, regen-source, revert-classify,
shared-checkout, skill-source and source-read, which the plan listed as denies because
their text names a command. Each fires on a condition its remedy does not clear, so a
deny would refuse every retry; the tests that pinned them showed it (a gated push refused,
a person's own wiring edit refused, every `git restore` refused). They deny once their
rule reads the remedy's effect.

### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. Advice nobody acts
  on is the warning nobody reads; a refusal with its remedy is the loud failure.
- [Built for the expensive era](../design/principles/built-for-the-expensive-era.md): relied on. A
  cheaper model follows a deny with a command in it; it does not weigh advice.
- [Automation you can interrogate](../design/judgment/automation-you-can-interrogate.md): relied
  on. `magus describe rules` lists each rule's default and the verdicts a workspace may
  give it, and a notice says why it cannot deny.
- [A record of refusals](../design/refusals.md): departs from ADR 0006
  section 2, which this supersedes. Eli decided this on 2026-10-21.

## Alternatives

- **Keep advice and measure it.** Lost: the measurements exist and say advice changed
  nothing (raw-tool, spawn-unbriefed, architecture-unbriefed above). Reopen if a rule's
  advice is shown to change the next call in most firings.
- **Deny every advisory, notices included.** Lost: a deny whose remedy does not clear it
  refuses every retry, which is G6 and G12. Reopen a notice when its rule reads whether
  the remedy happened, then make it a deny.
- **Ship advice and let each workspace raise rules to deny.** Lost: that is ADR 0006
  section 2, and only this repository did it. Every other workspace got the advice.
- **A new decision value, `notice`.** Not built: the guard engine grades advise and deny,
  and a notice behaves as advise that refuses deny, so the table marks it instead. Reopen
  if a notice needs grading the engine does not give advise.

## Consequences

- An agent in any workspace meets refusals it met only in this repository before. Each
  names its remedy; a workspace that disagrees sets the rule to advise by name.
- The tests that pinned advice set advise explicitly and so test the opt-out, and each
  renamed `...ByDefault` test now reads `...WhenSetToAdvise`.
- A bound worker running a raw tool is judged by worker-check-only first, so the refusal
  names the job's check rather than `magus run <target> <project>`. raw-tool answers only
  when the worker has no check naming that tool.
- The generated rule pages (docs/reference/rules) and the guard guide's tables change with
  the defaults; they are regenerated, never edited.

## Open questions

- dependency-update names `magus run <target>:update <project>`. A workspace with no
  update charm on a covering target has no remedy; should the rule stand down there?
- Which notices can read their remedy's effect (push-gate from the run log, skill-source
  from the skill-load markers) and become denies?
