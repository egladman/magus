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

magus.project({ "spells": [buf] });

export fun lint(args: [str]) > void {
    buf["buf-lint"]();
    buf["buf-breaking"]();
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
`internal/manpage/testdata/api.lock`, and `TestAPIUpToDate` regenerates it in
memory and compares. Change the CLI and the test fails until you regenerate:

```console
$ go generate ./internal/manpage/...
```

The regenerated diff is the review artifact. A new line is a new flag or command;
a removed line is a removed one. A reviewer reads the removed lines and decides
whether the change is acceptable, the same judgment `buf-breaking` automates for
protos.

To adopt this for your own tool: emit its public surface as a sorted list, commit
the list, and add a test that regenerates and compares. The list is derived from
one source of truth (magus builds it from the man page registry plus the config
keys), so it never drifts from the real CLI.

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
