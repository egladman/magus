---
title: Gate sizing
description: magus affected ci sorts every change into trivial, mechanical, scoped or full, prints the evidence for every file, and runs only the part of the gate the change can reach. Skip only what magus can prove.
tags: [ci, affected, risk, gate, merge-queue, go, redundancy, inheritance]
---

# Gate sizing

Most changes do not need the whole gate. `magus affected ci` works out which part
of it a change can reach and runs that, with no flag to ask for it:

```text
magus: ci gate sized scoped against 2d15f62c; override: --no-redundancy-check
  notes/release.md: trivial (prose: matches "**/*.md" (built-in default); nothing in ci's chain reads it)
  docs/guide.md: scoped (prose: read by docs:site-generate, which ci reaches)
  internal/ledger/report.go: scoped (code: Go package example.com/app/internal/ledger: its tests and every package importing it)
  gate: magus run lint . --no-default-charms && magus run build . --no-default-charms && magus run test . --no-default-charms && magus run site-generate docs --no-default-charms
  narrowed: go::go-test in . test runs 3 package(s): example.com/app/cmd/app example.com/app/internal/ledger example.com/app/internal/report
```

Every changed file gets the lowest tier magus can prove, and the change takes its
highest file's tier. A file magus cannot bound is `full`. When the change is
`full`, nothing is printed and the gate runs as it always has. Below `full`, the
block above goes to stderr before anything runs: every path with its tier, the
class it started from and the fact that decided it, then the commands that run
instead. The run records its verdict under `ci`, so the
[redundancy check](../../reference/codes/sandbox/MGS3010.md) and a job's
completion gate on `ci` read it like any other.

`--no-redundancy-check` runs the full gate: no deferral and no tier reduction.

## The tiers

Each path first gets a class: generated output, prose, a comment-only edit, or
code (see [workspace.md](../workspace.md) for `gate_low_risk`, which declares
prose). The tier builds on the class:

| Class        | Tier                                                                                                                                                                                                                                         |
| ------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| generated    | `trivial`, or `mechanical` when a file its generator reads changed in the same change, so the drift check re-derives it.                                                                                                                     |
| prose        | `trivial`; `scoped` when a target in `ci`'s chain declares it with `ctx.readsFiles` (a docs generator, a linter, a test that reads changelog fragments), or when a package compiles it in (`go:embed`).                                      |
| comment-only | `mechanical`, never `trivial`: lint and some generators read comments. `scoped` when a target in `ci`'s chain declares it (a conventions test reads comments too), which runs that reader beside the drift check and lint.                   |
| code         | `mechanical` when a language prover shows it means what the base meant (a Go file whose syntax tree, comments and formatting aside, equals the base's with the same directives); `scoped` when a prover places it in a package; else `full`. |

Every path is `full` while the change edits something the affected set itself is
computed from: a magusfile or spell, `magus.yaml`, a dependency manifest, a
toolchain pin. A file no project claims is `full` on its own, and the rest of the
change keeps its tiers.

| Tier         | Gate                                                                                                                                                                                                                                                                                                                                                                                     |
| ------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `trivial`    | None. `magus affected ci` lists every path and exits 0: a proof about this change, not a deferral.                                                                                                                                                                                                                                                                                       |
| `mechanical` | `generate` and `lint` for every affected project, `generate` left out where `lint` already needs it. `ci` strips `rw`, so `generate` is the drift check.                                                                                                                                                                                                                                 |
| `scoped`     | Every target in `ci`'s chain that declares a changed prose or comment-only file. For Go, `ci`'s chain for the project rooted at the module, its `test` target run with the go spell's `go-test` narrowed to the changed packages and every package whose build or tests import them, and `ci` for every other affected project. Without Go, the mechanical steps when a path needs them. |
| `full`       | `ci` for every affected project.                                                                                                                                                                                                                                                                                                                                                         |

## The Go prover

The Go prover is on whenever the go spell resolves in the workspace. It asks
`go list -e -test ./...` in each module the change touches, and a package is in a
scoped gate when the changed package is among its dependencies or its test
binary's. That is go's own answer, not magus's guess. Several things keep a Go
file at `full`:

- a load error anywhere in the module, since a package that failed to load may
  import the change without saying so;
- a package only `go:generate` programs reach, which is generator code;
- a file this platform does not build, or one no package compiles (testdata);
- a module no project is rooted at, which leaves nobody to run its tests.

The narrowed tests run inside the project's own `test` target, so the env
(`ctx.withEnv`) and flags (`-race`, coverage) its body gives `go-test` still apply.
Only the package list of each `go-test` call changes: its patterns resolve through
`go list`, and just the packages the change can reach are passed on. A call none of
whose packages can observe the change runs as written, so a body that expects each
call to leave its output behind (a coverage profile) still finds it. A narrowed run
never reads or writes the cache, since it is less than the target's key describes.

A check the body makes over the whole suite, such as a coverage floor, would judge
only the narrowed packages. `ctx.narrowed()` is true during a narrowed run, so the
body can stand the check down there; main's full gate still runs it:

```buzz
if (!ctx.narrowed() and coverage < 70) {
    throw "coverage is below the 70% floor";
}
```

A dry run and every run that is not a sized gate report `false`.

A member of `ci`'s chain that declares its inputs with `ctx.readsFiles` runs only
when the change touches one of them, which is when its cache key would move anyway,
and the report names each one it skipped.

## Where the tier is read

- **`magus affected ci`** sizes the gate, as above. That covers a local run and
  the [merge queue](../merge-queue.md)'s gate, which runs the same command.
- **`magus affected ci --plan`** emits an empty matrix and a `risk` block beside
  it when the change is `trivial`, so the workflow's `count > 0` guard skips every
  shard. Any other tier keeps the whole matrix: a shard runs
  `magus run ci <projects>`, and narrowing one waits on running several targets in
  one invocation.
- **The redundancy check** defers a gate only when the change since the green gate
  is `trivial` ([MGS3010](../../reference/codes/sandbox/MGS3010.md)).
- **CI verdict inheritance** skips the fan-out only when the change since the
  branch's last green run is `trivial` (`gate_inherit` in
  [workspace.md](../workspace.md)).
- **A job's completion gate** on `ci` passes on the branch's newest green `ci` gate
  when the change since it is `trivial`, even if that gate predates the job.

A merge settles a conflicted file by its class alone, not its tier; see
[auto-resolving source conflicts](../merge-queue.md#auto-resolving-source-conflicts).

## What sizing does not do

It does not guess. No tier rests on history: a target with a long clean record is
not skipped, because a record is not a proof about this change. A path magus
cannot read at the base (a new file, a shallow clone) is never proven
equivalent, and a prover that fails leaves every path it would have placed at
`full`. A prose or comment-only file is read only by what declares it: a target that reads a file
it never declared with `ctx.readsFiles` would miss it here, as its cache key
already does.
