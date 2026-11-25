---
title: Stale is never silent
order: 13
description: A stale output, a replayed result or a fallback is reported as a coded diagnostic with a fix line, and each cause gets its own code.
tags: [design, judgment, diagnostics, generated, cache]
---

# Stale is never silent

When magus does something the command did not say it would, it reports it with
a diagnostic code and the command that fixes it. A generated output that no
longer matches its sources, a result replayed from the cache where the reader
expected a fresh run, a fallback taken, a step skipped: each is coded,
[documented](../../reference/diagnostics.md), and ends with a line the reader
can run.

A code names one cause. When two causes leave the same symptom, they get two
codes, because the fixes differ. A generated file that drifts while its
declared inputs are unchanged is toolchain skew
([MGS4005](../../reference/codes/race/MGS4005.md)), and the fix is to discard
the drift. One that drifts after a declared input changed is stale
([MGS4006](../../reference/codes/race/MGS4006.md)), and the fix is to
regenerate and commit. A generator that reads an input it never declared and
one that writes different bytes from the same inputs are two more causes with
two more fixes. A hint that can name only one of them sends the reader after
the wrong one.

The reason is who reads the message. Whatever trips a person trips an agent,
and the agent is worse placed to notice: it has no memory of last week's run to
compare against, and it reads a quiet exit 0 as proof. On 2026-11-24
`magus affected generate:rw` wrote stale job bindings without a word, and the
MGS4006 hint that followed could suggest only a nondeterministic generator.
Finding the real cause took reading the generator. A diagnostic that had named
it would have taken one command.

[No silent defaults](no-silent-defaults.md) says a fallback says that it fell
back. This page says what it says: a code, a fix line, and a different code for
a different cause.

## Enforced by

- The `diagnostic-codes-have-docs-pages` lint fails a code with no page under
  [the diagnostics reference](../../reference/diagnostics.md), so every code a
  reader meets carries a resolution.
- Regeneration drift is split by whether a declared input changed: MGS4005 for
  skew, MGS4006 for a stale output. The MGS4006 notice ends with
  `magus run generate:rw <projects>` and the VCS command that folds the fix
  into the commit that caused it (`buildDriftNotice` in
  [`internal/changeset/drift.go`](https://github.com/egladman/magus/blob/main/internal/changeset/drift.go)).
- Not enforced yet, and so a debt in [Where this is strained](../strained.md):
  no code tells an undeclared generator input from a nondeterministic
  generator.

## Good

The MGS4006 notice from `buildDriftNotice` names the cause, the projects and
both commands:

```text
[MGS4006] commit 3f2c9a1 left generated output stale (docs changed with no matching regeneration).
Regenerate: magus run generate:rw docs
Then fold it into 3f2c9a1 (unpushed, still HEAD):
  git commit --amend --no-edit
```

## Bad

```text
generated output drifted; the generator may be nondeterministic
```

It names one cause of several, so the reader chases a generator bug while the
real cause, an input the generator reads and never declares, stays where it
is.

## See also

- [No silent defaults](no-silent-defaults.md)
- [A refusal carries its reason](a-refusal-carries-its-reason.md)
- [Human-first is the AI integration](../principles/human-first-is-the-ai-integration.md)
- [Diagnostics](../../reference/diagnostics.md)
