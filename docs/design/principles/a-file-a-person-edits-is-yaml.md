---
title: A file a person edits is YAML
order: 41
description: A file a person adds to or edits is YAML, grouped and commented; JSON Lines is append-only state a machine writes. Analyzer policy is the repository's, on top of magus.
tags: [design, principle, state, policy]
---

# A file a person edits is YAML

A file split two ways: who writes it decides its format. A file a person adds
to or edits by hand is YAML. A file a machine appends to is JSON Lines, as
[Everything is a file](everything-is-a-file.md) sets out. If a person is
expected to maintain it, it is not JSON Lines.

JSON Lines suits a machine: one complete record per write, the last line per
key wins, a torn write costs one line. It reads badly to a person maintaining
it. Every line repeats every key, nothing groups related entries, and a reason
can sit only in a field, never in a comment above a block. YAML gives a
person nesting, comments and a key per thing, and a strict decoder still
refuses a misspelled field or a repeated key.

Analyzer policy follows from this. What a repository excuses (an analyzer's
settings, its baseline, its exits, its engineered zones) is that repository's
call, kept in a file the repository owns. It is layered on top of magus and
never compiled into the binary or carried in `magus.yaml`. Like every other
tool's config, it is contextual: each project root holds its own
`analyzers.yaml`, and an analyzer judges a file by the `analyzers.yaml` of the
project that owns it, resolved the way magus resolves a project. There is no
repository-wide file, and no fallback to another project's: a project with no
`analyzers.yaml` has no baseline and no exits, and gets the strict defaults.
One file for every project would hide which rules apply where.

- Paths in a project's file are relative to that project's root.
- One top-level key per analyzer, holding `settings`, a `baseline` of today's
  violations that may only shrink, and `exits`, each with a required `reason`.
- One top-level `zones` list: a path whose complexity a measurement licenses,
  with its `reason` (`spec`, `performance` or `security`), its `evidence` and
  its `budgets`.
- A stale baseline entry fails, so the baseline shrinks as code is fixed. An
  agent's job never lists the file in its write paths, so the guard denies an
  agent's edit to it and only a person grows it.

**Debt.** Person-edited JSON Lines found on 2026-11-18, owed a move to YAML:

- `proto/buf-breaking.deviations.jsonl`: a reason per accepted break. It waits on the
  `internal/guard` tests that name it.
- `<project>/accepted.jsonl`, the per-change ledger
  [ADR 0021](../../decisions/0021-a-change-accepts-the-findings-it-causes.md)
  proposes: amend the proposal before it is built.

Judged machine-written, so JSON Lines stays right: `testdata/retrieval/judgments.jsonl`
(harvested gold judgments) and `libs/proofread/testdata/kind-rules.jsonl` (a pin the
kinds test regenerates with `KIND_PIN_UPDATE=1`).

## Enforced by

- `accessorclone` reads the `analyzers.yaml` at the root of the module it lints
  and decodes its key strictly: an unknown field, an accessor listed twice, an
  exit with no reason or a stale baseline entry fails.
- `no-throw` judges each file against its own project's `analyzers.yaml` only,
  and fails a count above what a file holds, an exit with no reason and an exit
  that matches no throw.
- Nothing refuses a new person-edited `.jsonl` file yet, so the format holds in
  review alone.

## Good

```yaml
accessorclone:
  baseline:
    spells/spell.go:
      - Spell.Outputs
      - Spell.Sources
  exits:
    - path: types/output.go
      symbol: StoredOutput.Bytes
      reason: the caller owns the bytes by contract
```

## Bad

```jsonl
{"path":"spells/spell.go","symbol":"Spell.Outputs"}
{"path":"spells/spell.go","symbol":"Spell.Sources"}
{"path":"types/output.go","symbol":"StoredOutput.Bytes","reason":"the caller owns the bytes by contract"}
```

A person adding an entry copies a line and edits it. Entries for one file
scatter, and nothing marks which line is debt and which is a decision.

## See also

- [Everything is a file](everything-is-a-file.md)
- [Behavior is code, facts are data](behavior-is-code-facts-are-data.md)
- [Human-first is the AI integration](human-first-is-the-ai-integration.md)
