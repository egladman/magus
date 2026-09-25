---
title: Risk tiers
description: magus affected <target> --risk sorts a change into trivial, mechanical, scoped or full, names the evidence for every file, and prints the reduced gate that suffices for it.
tags: [ci, affected, risk, gate, merge-queue, go]
---

# Risk tiers

Most changes do not need the whole gate. `magus affected <target> --risk` says
which ones, and what they need instead:

```sh
magus affected ci --risk --base origin/main -o json
```

It runs nothing. The report carries the change's `tier`, one `evidence` line per
changed file saying why it landed where it did, and `gate`: the magus commands
that are sufficient for that tier, each with its `argv`. `<target>` is what a
full gate runs. `--stdin` reads the changed paths from a pipe instead of a diff;
add `--base` to it and base-side content is read there, otherwise nothing can be
proven comment- or format-only.

## The tiers

A file gets the lowest tier magus can prove, and the change takes its highest
file's tier. A file magus cannot bound is `full`.

| Tier         | A file lands here when                                                                                                                                                                                                                                                                | Gate                                                                                                                                                                                                                                                                                                                            |
| ------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `trivial`    | It is prose (the `gate_low_risk` globs, markdown by default) that no Go package embeds, or a declared generated output whose generator reads nothing else the change touched.                                                                                                         | None.                                                                                                                                                                                                                                                                                                                           |
| `mechanical` | It is Go whose syntax tree, comments and positions aside, equals the base's with the same directives (`//go:build`, `//go:embed`, cgo's preamble), or comment-only in a language whose spell declares its comment syntax, or a generated output regenerated beside an input it reads. | `generate` and `lint` for every affected project, `generate` left out where `lint` already needs it. Both run with `--no-default-charms`, so `generate` is the drift check.                                                                                                                                                     |
| `scoped`     | It belongs to a Go package, as a compiled file, a test file or an embedded file, in a module `go list` loads cleanly.                                                                                                                                                                 | The invoked target's chain for the module's project, with its `test` target, when that runs the go spell's `go-test` op, replaced by `go::go-test-packages` over the changed packages and every package whose build or tests import them, followed by `test`'s own chain. Every other affected project runs the invoked target. |
| `full`       | Anything else, and always Buzz (magusfiles, spells, tools), a declared input of a target the `generate` target reaches, a package only `go:generate` programs reach, a file go does not build here, and any file in no module.                                                        | The invoked target for every affected project.                                                                                                                                                                                                                                                                                  |

The package set is `go list -e -test ./...`'s own answer: a package is in it when
the changed package is among its dependencies or its test binary's. A load error
anywhere in the module makes every Go file in it `full`, because a package that
failed to load may import the change without saying so.

`go::go-test-packages` is `go test` with no `./...` default. `magus run
go::go-test -- <pkgs>` cannot narrow anything, because forwarded args land after
the default and `go test ./... <pkgs>` is the whole module.

The scoped gate reproduces the test run, not everything the project's test target
does around it: flags it passes (`-race`, coverage) and checks over the whole tree
(a coverage floor) are left to main's CI. A chain member that declares its inputs
with `ctx.readsFiles` runs only when the change touches one of them, which is when
its cache key would move anyway; the report lists each one it skipped. A member
other than `test` that also runs `go-test` chose its own packages, so it runs as
declared.

## Statistical pruning

On top of the proven tiers, a `scoped` or `full` gate drops a (project, target)
pair whose recorded history is clean: at least `ci.risk_min_runs` affected runs
inside `ci.risk_window`, and not one failure among them. A volatile outcome (failed,
then passed on a retry) counts as a failure, and a zero-duration outcome, which is
a spell answering for a target it does not implement, counts as nothing. Every
pair's rate, run count and window lands in the report's `risk`, and every dropped
pair is listed in `evidence` with its numbers.

```yaml
ci:
  risk_min_runs: 50 # 0 turns pruning off
  risk_window: 720h # must be positive while pruning is on
```

This is the one part of the report that is not a proof. The history records
outcomes per project and target, not which files each run's change touched, so
"runs on changes like this one" means runs where the project was affected, and no
per-package history exists. Pruning therefore relies on main's post-merge CI,
which always runs the full gate, to catch a miss, and the
[merge queue](../merge-queue.md) already freezes merges onto a red main. A pair
with thin or missing history is never pruned; it keeps the proven tier's gate.
