---
title: "ADR 0053: an op declares the files it edits in place"
order: 53
description: A spell op that rewrites files in place names them in an `edits` list on its Command. When the op is the step (`magus run go-fmt:rw`, `go::go-fmt:rw`), the globs key the step and exempt those files from MGS4007, as `ctx.writesFiles(..., editsExistingSource: true)` does for a target. A magusfile target that composes the op inherits nothing and keeps declaring its own writes. The check variant still fails on drift.
tags: [adr, decision, spells, charms, cache, MGS4007, formatter]
status: accepted
date: 2026-11-19
---

# ADR 0053: an op declares the files it edits in place

## Context

Running a rewriting op directly failed after its write landed. In a project bound to
the go spell, with `p.go` unformatted:

```text
$ magus run go-fmt p          -> [fail] gofmt exited 1                (the check fails on drift)
$ magus run go-fmt:rw p       -> [fail] [MGS4007] p:go-fmt modified its declared sources p/p.go;
                                 declare them with ctx.writesFiles([...], editsExistingSource: true)
$ cat p/p.go                  -> formatted                            (the rewrite was kept)
```

- The verdict depended on how many times the command was typed: a second identical
  run passed, because nothing was left to rewrite.
- The remedy could not be followed. `ctx.writesFiles` is a call in a magusfile target
  body, and a bare op has no body.
- The docs advertised runs that failed this way: `magus run go::golangci-lint:rw,debug`
  in [charms](../concepts/charms.md), `go-mod-tidy:update` in the go spell, and
  `typos:fix` on the [MGS4007](../reference/codes/race/MGS4007.md) page.

Every op that rewrites in place failed the same way when run directly: go-fmt,
golangci-lint, go-mod-tidy, go-mod-edit, biome, eslint, prettier, ruff, cargo-fmt,
buf-format, dprint, typos and latexindent. Installs passed, because `installStep`
already derives the files an install rewrites from typed spell data (`install.go`).

MGS4007 itself was right. It compares the files a step declares it edits with the
files that changed, whatever charm is active. It lacked a place for an op to declare
files.

## Decision

1. `spells.Command` gains `edits`: project-relative globs, `!` exclusions included,
   of the existing files the op may rewrite in place. The list is unconditional, as
   the MGS4007 page asks of a target: it says what the op may touch, and a charm
   decides whether it does. `Command.Validate` checks each glob. The field is
   serialized, so it is in `BuiltinsHash`.
2. When a step is the op itself, its `edits` fold into `Step.Sources` and
   `Step.Updates`, rooted at the project (`foldOpEdits`, `run.go`). Like
   `editsExistingSource`, the files key the step and are never snapshotted, replayed
   or cleaned.
3. "The step is the op" follows the dispatch exactly (`directOps`, `run.go`). With no
   filter it is every resolved spell's op of that name, unless a magusfile export
   shadows it (`magusfileOverride`), in which case the body runs and nothing folds.
   Under a `spell::op` filter it is the named spell's op, even where an export shadows
   the name, because `makeSpellFilteredHandler` bypasses the shadow. `buildStep` folds
   the unfiltered dispatch and the run loop adds the filtered one beside `step.Spell`.
4. A magusfile target that composes the op inherits nothing. Its body still declares
   its own writes. The op cannot know which of its edits the target's call leaves in
   force, and inheriting them would make every `lint` that calls golangci-lint a
   declared writer that a local ci repairs under
   [ADR 0048](0048-a-local-ci-repairs-a-declared-writer.md).
5. An op's `edits` skip the rule that an edited source must already exist
   (`checkEditedSourcesExist`). That rule reads `ctx.writesFiles` declarations only.
   `go.sum` may not exist before the first tidy, and the glob describes a kind of
   file, not one file.
6. MGS4007 keeps its code and its rule. For a step that is the op (`Step.Ops`), the
   message names the op's edits field instead of `ctx.writesFiles`: `declare them in
   the edits of op go::go-fmt, or run the op from a target that declares them with
   ctx.writesFiles([...], editsExistingSource: true)`.

| Item                                                                         | State   |
| ---------------------------------------------------------------------------- | ------- |
| `Command.Edits`, validated, in the Buzz mirror and `BuiltinsHash`            | done    |
| The fold for a bare op, following the filter and the shadow                  | done    |
| MGS4007 names the op's field for a bare-op step                              | done    |
| `edits` on every shipped op that rewrites in place                           | done    |
| `opEditsSuite` (`run_test.go`): check fails on drift, rw passes, composer    | done    |
| The guard reads `edits` in place of "this op writes, set unknown"            | planned |
| The doctor compares an op's `edits` with a composing target's `writesFiles`  | planned |
| `order.go` orders readers after a bare op's edits                            | planned |

### What a run does now

- `magus run go-fmt` over a drifted file fails with gofmt's own exit, writes nothing
  and records nothing.
- `magus run go-fmt:rw` rewrites the file and passes. `movedInputs` refuses the entry,
  because an input moved during the run. The next `go-fmt:rw` changes nothing and
  records, and the one after that replays.
- After a recorded passing check, a new edit to the file changes the key, because the
  file is in Sources. The check misses and fails on the new drift.
- An op that rewrites a file outside its `edits`, or declares none, still fails MGS4007.

### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): the write set is
  declared on the op and rendered with it. Nothing is inferred from a charm's name.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md):
  for a bare op, MGS4007 names a field the reader can set.
- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  `edits` is static data beside `bin`, `args`, `sources` and `envKeys`.
- Mechanisms live below the magusfile: no workspace writes a
  wrapper target to make a shipped op runnable. The spell holds the globs and core
  folds a list, so core stays language-agnostic.
- [Sit next to the tools](../design/principles/sit-next-to-the-tools.md): `go-fmt:rw`
  is the tool's own act, and it can now be run directly.
- [Friction that carries meaning](../design/principles/friction-that-carries-meaning.md):
  the old refusal told every caller the same sentence.
- [A cache is an accelerator](../design/principles/a-cache-is-an-accelerator.md):
  `movedInputs` still decides what is stored. No run that rewrote is recorded.
- [The checker catches it](../design/principles/the-checker-catches-it.md): with the
  op's write set declared, the doctor can compare it with a composing target's at
  check time. That comparison is planned.
- [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  every cache key moves once, and `changes/unreleased/op-edits.md` says so.

## Alternatives

### The charm declares its rewrites

`"rw": Charm{ops = ..., edits = ["**/*.go"]}`.

- Why it lost: a charm "edits arguments only" ([charms](../concepts/charms.md)), and the
  MGS4007 page says a charm cannot say whether the tool writes. The check would have to
  read the active charm set, so it would stop being charm-agnostic. Static readers
  (Sources, ordering, the guard, `describe file`) cannot depend on the charm set, so one
  declaration would be read two ways. It buys one report: a check variant that writes a
  declared file. Targets have never had that report.
- What would reopen it: a shipped op whose check variant writes a file its rw variant
  also edits.

### rw means "may rewrite its declared sources"

- Why it lost: the MGS4007 page rejects it by name. With `default_charms: [rw]` every
  local run is exempt, which brings back the typos incident. It says nothing about
  `update` or a workspace's own charms, and it widens the exemption to the whole
  baseline, so go-mod-tidy could rewrite `**/*.go`.
- What would reopen it: none; this is the recorded rejection.

### Keep it: workspaces wrap each op in a target

- Why it lost: each workspace types the glob list the spell already knows, per op per
  project. This repository does it 19 times, and the copies disagree about `go.sum`.
  The defect stays for every consumer workspace.
- What would reopen it: none while an op can declare its writes.

### An op inherits into the targets that compose it

- Why it lost: decision 4. It would delete the 19 restated lists, as `EnvKeys` does for
  the environment. It would also turn every check target that calls a fixer into a
  declared writer that a local ci repairs.
- What would reopen it: a way for a composing call to say which of the op's charms it
  leaves active.

## Consequences

- `go-fmt:rw`, `go-mod-tidy:update`, `typos:fix` and the rest run directly and pass.
  The run that rewrote is not recorded; the next one is.
- Every cache key moves once, because the registry hash covers `edits`.
- A third-party spell whose op writes and declares nothing still fails MGS4007, now
  with a remedy it can follow.
- The 19 magusfile glob lists stay. Each target still answers for the ops it composes.
- A `spell::op` filter that drops one of two spells sharing an op name keeps the other
  spell's edits folded. That can only over-invalidate.

## Open questions

- Whether a bare-op step under `update` should be `NoCache`, as an install is, rather
  than recording at its fixed point as `rw` does.
- When the guard and the doctor start reading `edits`.
