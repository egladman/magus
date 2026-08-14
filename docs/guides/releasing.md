---
title: Releasing
description: Cut version tags for one or more workspace modules from your own machine with magus run release, what the dry run shows, and which tags the release workflow reacts to.
tags: [release, versioning, tags, go-modules, workflow]
---

# Releasing

You release from your machine. `magus run release` cuts the tags. Nothing in CI
does it for you, and nothing pushes on your behalf.

| Step                | Where                             | What it does                                 |
| ------------------- | --------------------------------- | -------------------------------------------- |
| Cut the tag         | your machine, `magus run release` | tags each module, rewrites the root `go.mod` |
| Push the tag        | your machine, `git push`          | deliberate, separate, yours                  |
| Build the artifacts | `release.yaml` on GitHub          | archives, images, signatures                 |

## Survey first

Run it with no arguments. It tags nothing and prints every module, where it sits,
and the three legal next versions:

```bash
magus run release
```

```text
release: magus - at v0.3.0, next: patch v0.3.1 | minor v0.4.0 | major v1.0.0
release: libs/gopherbuzz - unreleased, next: patch v0.0.1 | minor v0.1.0 | major v1.0.0
release: libs/testlayout - unreleased, next: patch v0.0.1 | minor v0.1.0 | major v1.0.0
```

magus computes the three candidates and stops there. Which one a change deserves
depends on what breaks for a consumer, so you pick.

The module list is computed too, from every project carrying a manifest. A new
module shows up the moment it becomes a project, and there is no list to maintain.

## Release one module

Name it as `<module>@<version>`, using the key the survey prints:

```bash
magus run release:cd -- libs/testlayout@0.1.0
```

Drop the `cd` charm for a dry run. It runs every check, prints every action, and
changes nothing:

```bash
magus run release -- libs/testlayout@0.1.0
```

```text
release: DRY RUN - add the cd charm to execute
release: tag libs/testlayout/v0.1.0
release: dry run only - nothing was tagged or written
```

## Release several at once

Pass as many pairs as you like. Anything you leave unnamed gets surveyed and
skipped:

```bash
magus run release:cd -- magus@0.4.0 libs/gopherbuzz@0.2.0
```

Each module keeps its own cadence, because Go already gives each its own tag
namespace. Deriving every tag from one number would chain a library's public API
to the root's release schedule.

## The order it enforces

Releasing the root alongside a nested module has an order that is easy to get
wrong by hand, so the target handles it:

1. **Nested modules first** (`libs/<name>/vX.Y.Z`). Each has to be `go get`-able
   before anything can require it at a real version.
2. **Rewrite the root `go.mod`**, pointing each `require .../libs/<name>` from
   `v0.0.0` at the version just tagged.
3. **Tag the root last** (`vX.Y.Z`), so its tag captures that rewrite.

Reverse it and the root tag ships a `go.mod` requiring versions that do not
exist, which breaks `go get github.com/egladman/magus` for everyone outside the
repo.

The `replace` directives stay untouched. `replace` is never transitive, so no
consumer downstream sees them, and they are the standard pattern for local
development inside one repo.

A module the root's `go.mod` does not require is tag-only, so step 2 skips it.
That covers a module in another language, and equally a Go module nothing imports:
`libs/testlayout` gets compiled into a golangci-lint binary, so the root has no
require line to point anywhere.

## What it refuses

Every check runs before the first tag exists, so a rejected release cannot leave
half the modules tagged:

- **A version that is not a legal next one.** Patch, minor, or major successor to
  the current version. A prerelease or `+metadata` build of one of those passes.
- **A tag that already exists**, checked across every named module first.
- **An unknown module key**, so a typo cannot release nothing and report success.
- **A major bump Go cannot resolve.** From v2 on, Go wants the module path to end
  in `/vN`, and `go get` resolves the path rather than the tag. A `v2.0.0` tag
  beside a `go.mod` still declaring the v1 path is uninstallable. Renaming a
  module means rewriting every import, so the release stops rather than guesses.
- **A dirty tree**, under `cd`: `release: tree is dirty; commit before releasing`.
  The dry run warns instead of failing, so you can rehearse.

## Pushing

Nothing above pushes. Review the tags, then push when you mean it:

```bash
git push origin v0.4.0
```

### Prereleases and the image channel

`release` takes no channel charm, because a version tag has only one channel.
Container images do have channels, and `release.yaml` picks one with `tagged`:

```bash
magus run image-build:cd,tagged
```

`tagged` reads the channel off the release tag HEAD sits on. A stable semver takes
`latest`; `v0.5.0-rc.1` gets its version tag alone. The derivation lives in the
magusfile's `channel()`, not in YAML, so the image step and the prerelease flag on
the GitHub release cannot disagree about what is shipping.

Cutting a release candidate is therefore a tag and nothing else. You can still name
`stable` or `unstable` directly for a local build, but not beside `tagged`, which
already answers that question:

```bash
magus run image-build:cd,unstable   # version tag only, no floating tag
magus run image-build:cd,stable     # version tag plus latest
```

An untagged HEAD is refused rather than guessed at:

```text
image: `tagged` reads the channel off the release tag HEAD sits on, and HEAD is not
tagged - `v0.4.0-3-gabc123` describes distance past the nearest tag rather than a
release.
```

### Which tags the workflow reacts to

Only a root `vX.Y.Z` tag triggers the release workflow. `release.yaml` matches
`v*`, and `libs/testlayout/v0.1.0` does not start with `v`, so pushing it
publishes no binaries and no images. A library tag exists to make the module
resolvable by `go get`, which needs no artifacts. Push it like any other tag and
consumers can require it.
