---
title: Friction that carries meaning
order: 13
description: Keep resistance where the resistance is the information; friction that tells every caller the same sentence is a toll, and a toll is removed.
tags: [design, principle, cli, friction]
---

# Friction that carries meaning

A tool made perfectly smooth stops telling you what it did. Every act collapses
into the same keystroke, and a pull, a push, a delete and a cache hit read
alike. magus keeps resistance where the resistance is the information, after
[Designing Friction](https://designingfriction.com/) by Luna Maurer and Roel
Wouters, with Alexandra Barancova. Their five moves map onto this tool as
follows.

- **Discomfort.** Output states the uncomfortable fact rather than a
  reassuring one. A lookup the index cannot vouch for answers "unknown, not
  absent" ([`cmd/magus/stale_index.go`](https://github.com/egladman/magus/blob/main/cmd/magus/stale_index.go)). A target past its ceiling fails with
  [MGS3011](../../reference/codes/sandbox/MGS3011.md) and is never a skip. A
  setting magus cannot honor fails the command, as
  [No silent defaults](../judgment/no-silent-defaults.md) requires.
- **Time delay.** A pause sits in front of an act that cannot be taken back,
  never inside the loop that verifies. The first Ctrl+C only arms a
  three-second window (`confirmWindow`, [`cmd/magus/interrupt.go`](https://github.com/egladman/magus/blob/main/cmd/magus/interrupt.go)). The
  `push-ungated` guard rule stops a push at a commit no green gate has run
  on.
- **Engage the body.** The verb names the physical act, and the person does it
  with their own hands. Registry transfers are `graph pull` and `spell pull`,
  never a side effect of another verb. The acts in
  [Manual on purpose](../judgment/manual-on-purpose.md) stay a person's keystroke.
- **Non-positive.** magus publishes what it refused to build and why, in
  [A record of refusals](../refusals.md), and a budget it cannot fit is
  refused at once ([MGS3009](../../reference/codes/sandbox/MGS3009.md)) rather than
  queued behind a spinner.
- **Unpredictable self.** A refusal hands back the command to run, so the
  person decides the next move instead of following a default the tool picked
  for them ([A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md)).

The test for any new resistance is whether it carries information. The
`push-ungated` catalog entry names the failure it replaced: an advisory that
fired on every push having read nothing was "a toll rather than a reminder".
Friction that tells the fast caller and the careless caller the same sentence
is a toll, and a toll is removed. A finding charged to a change that did not cause
it is a toll too, so whoever adds or tightens a rule pays its cost
([Rolling out a stricter rule](https://github.com/egladman/magus/blob/main/CONTRIBUTING.md#rolling-out-a-stricter-rule)).
This principle never slows
[the loop that verifies](optimize-the-loop-that-verifies.md):
a cache hit stays instant, and the pause belongs to the act that leaves the
machine.

**The Go SDK.**

The [Go SDK](../../reference/go-sdk.md) has been read against this page, and
holds these rules:

- Every verdict comes back with its reason and its rule: a
  `types.GuardJudgment` names the rule that decided, a `JobStatus` lists its
  violations, and a knowledge answer keeps unknown apart from absent.
- No silent default. A workspace with a magusfile and no linked engine fails
  with [MGS1104](../../reference/codes/capability/MGS1104.md), and a library
  workspace reads no environment unless it is given `WithEnv`.
- No helper hides a refusal: the SDK never retries, forks, escalates or
  re-grades on the caller's behalf.
- Ambient state appears in the signature. The lease rides `ctx` through
  `types.WithJobLease`, the session is `WithSession`, the environment is
  `WithEnv`, and stderr is `WithStderr`.
- A swappable part is a small interface with one option (`WithVCSDriver`,
  `WithObserver`, `WithCacheBackend`), built only where a second
  implementation exists.
- The root package grows only by wrapping code that already exists.

The debt is the rest of magus: the console has not been read against this
page, and until it is, its smoothness is unexamined rather than chosen.

## Enforced by

- The `push-ungated` guard rule, `confirmWindow` in [`cmd/magus/interrupt.go`](https://github.com/egladman/magus/blob/main/cmd/magus/interrupt.go),
  and the coded refusals linked above.
- [Rolling out a stricter rule](https://github.com/egladman/magus/blob/main/CONTRIBUTING.md#rolling-out-a-stricter-rule):
  a new rule never charges a change that did not cause its finding.

## Good

The `push-ungated` rule fires only on the act that leaves the machine, and only
when the evidence is missing. Its catalog entry ([`internal/guard/catalog.go`](https://github.com/egladman/magus/blob/main/internal/guard/catalog.go)):

```text
push-ungated: a push or `gh pr create` at a commit with no green gate; refused for every session
```

## Bad

```text
Reminder: make sure your tests pass before pushing!
```

It fires on every push whether or not a gate ran, so the careful caller and the
careless one read the same sentence, and both learn to skip it.

## See also

- [A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md)
- [The loop keeps you able to notice](../judgment/the-loop-keeps-you-able-to-notice.md)
- [Manual on purpose](../judgment/manual-on-purpose.md)
