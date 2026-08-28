---
title: Breaking changes
description: How magus makes backward-incompatible changes visible in review before they reach users - buf-breaking for proto schemas and a drift-gated api.lock snapshot for a CLI's public surface.
tags:
  [
    breaking-changes,
    compatibility,
    buf-breaking,
    api-lock,
    drift-gate,
    changelog,
    proto,
  ]
---

# Breaking changes

A backward-incompatible change should show up in a pull request diff, not in a bug
report after release. magus gives you two mechanisms for this, one per artifact you
publish: `buf-breaking` for a protobuf schema, and a drift-gated `.lock` snapshot
for a command-line surface. Both turn "did this change break a consumer?" into a
diff a reviewer reads, so nobody has to remember to check.

## Proto schemas: buf-breaking

The `buf` spell ships a `buf-breaking` op that compares your current `.proto`
schema against a baseline and fails on a wire- or JSON-incompatible edit (a renamed
field, a changed type, a deleted message). It defaults to the `main` branch, buf's
standard CI baseline:

```buzz title="spells/examples/buf/buf-breaking.buzz"
import "magus";
import "magus/spell/buf";

magus\project({ "spells": [buf] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    buf["buf-lint"](ctx);
    buf["buf-breaking"](ctx);
}
```

Compose it into the read-only `lint` target alongside `buf-lint`, `go-vet`, and the
rest. `magus run lint` then forks `buf breaking --against .git#branch=main`, and a
breaking `.proto` edit fails the same stage that catches a style violation. Point
the baseline elsewhere with a function target when a repo uses a different default
branch or an image baseline.

## CLI surfaces: a drift-gated api.lock

A proto schema has buf to describe its compatibility. A command-line surface has
nothing equivalent, so magus tracks its own with a pattern you can copy for any CLI
you ship.

`magus-utils api` writes the public surface (every subcommand, flag, project
target, and config key) as a sorted, newline-delimited `.lock` file, the same flat
format as `urls.lock`. The snapshot lives at
`internal/cli/testdata/api.lock`, and `TestAPIUpToDate` regenerates it in
memory and compares. Change the CLI and the test fails until you regenerate:

```console
go generate ./internal/cli/...
```

The regenerated diff is the review artifact. A new line is a new flag or command;
a removed line is a removed one. A reviewer reads the removed lines and decides
whether the change is acceptable, the same judgment `buf-breaking` automates for
protos.

To adopt this for your own tool: emit its public surface as a sorted list, commit
the list, and add a test that regenerates and compares. The list is derived from
one source of truth (magus builds it from the man page registry plus the config
keys), so it never drifts from the real CLI.

## Magusfile API: a locked namespace surface

A magusfile is the third surface magus publishes, and it fails differently from the
other two. Buzz reads a missing member as `null` rather than erroring, so deleting a
binding breaks nothing at load: a magusfile still calling it parses, loads, and passes
`magus ls`, then fails at run time with `buzz: null is not callable` - a message that
names neither the call nor its replacement. Worse, magus builds the target dependency
graph by reading `ctx.needs` statically, so a magusfile calling a removed `needs` form
reports no dependency edge at all and simply stops running its prerequisites.

Two things keep that from happening quietly.

[MGS1025](../reference/codes/magusfile/MGS1025.md) rejects a known-removed call at
load, naming what replaced it. The calls it knows are a table in
`internal/interp/runtime.go`.

That table is hand-maintained, so a lock file makes the next removal impossible to
make silently. `internal/interp/bindings/testdata/magus-api.lock` is a sorted snapshot
of every member a magusfile can reach on the magus namespace, and
`TestMagusSurfaceLocked` rebuilds the namespace and compares. Delete a binding and the
test fails naming the member, pointing at the table that has to describe it:

```text
magus.needs was REMOVED from the magusfile surface.
Add it to removedMagusfileAPI in internal/interp/runtime.go so it is rejected at
load with MGS1025, document it in docs/reference/codes/magusfile/MGS1025.md, then
regenerate this lock
```

Regenerate it the same way you would any other snapshot:

```console
UPDATE_MAGUS_API_LOCK=1 go test ./internal/interp/bindings/
```

A companion test asserts the reverse, that no table entry names a member the namespace
still binds, so the diagnostic cannot start rejecting a call that works.

## Acceptance model

magus deliberately keeps this lightweight. There is no allowlist file to maintain
and no `doctor` check that a machine has to interpret. Acceptance is a human reading
a diff:

1. The drift gate fails on any surface change, breaking or not.
2. You regenerate, and the `.lock` diff joins the pull request.
3. A reviewer reads it. An added line needs no ceremony. A removed line is a
   backward-incompatible change, so you record it as a `### Breaking` note under
   `## [Unreleased]` in [CHANGELOG.md](https://keepachangelog.com/en/1.1.0/).

The `CHANGELOG` note is the release story; the `.lock` diff is the proof. Neither
requires a new subcommand or a config flag, because the compatibility record is the
same review every other change already goes through.
