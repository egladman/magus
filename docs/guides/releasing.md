---
title: Releasing
description: Cut version tags for one or more workspace modules from your own machine with magus run release, what the dry run shows, and which tags the release workflow reacts to.
tags: [release, versioning, tags, go-modules, workflow]
---

# Releasing

Releasing happens on your machine. `magus run release` cuts the tags; nothing in
CI does it for you, and nothing is ever pushed on your behalf.

This is the local half of a two-part split:

| Step | Where | What it does |
| --- | --- | --- |
| Cut the tag | your machine, `magus run release` | tags each module, rewrites the root `go.mod` |
| Push the tag | your machine, `git push` | deliberate, separate, and yours |
| Build the artifacts | `release.yaml` on GitHub | cross-compiled archives, images, signatures |

## Survey first

Run it with no arguments. It releases nothing and prints every module, where it
is now, and the three legal next versions:

```bash
magus run release
```

```text
release: magus - at v0.3.0, next: patch v0.3.1 | minor v0.4.0 | major v1.0.0
release: libs/gopherbuzz - unreleased, next: patch v0.0.1 | minor v0.1.0 | major v1.0.0
release: libs/orphanator - unreleased, next: patch v0.0.1 | minor v0.1.0 | major v1.0.0
```

The three candidates are computed, never chosen for you. Which one a change
deserves is a judgment about what breaks for a consumer, and that stays with the
developer.

The list is computed too. It is every project carrying a manifest, discovered
through `magus ls`, so a new module appears here the moment it becomes a project.
There is no list to maintain.

## Release one module

Name it as `<module>@<version>`. The module key is the same string the survey
prints:

```bash
magus run release:cd -- libs/orphanator@0.1.0
```

Without the `cd` charm this is a dry run. It performs every check, prints every
action, and changes nothing:

```bash
magus run release -- libs/orphanator@0.1.0
```

```text
release: DRY RUN - add the cd charm to execute
release: tag libs/orphanator/v0.1.0
release: dry run only - nothing was tagged or written
```

## Release several at once

Pass as many pairs as you like. A module you do not name is surveyed and left
alone:

```bash
magus run release:cd -- magus@0.4.0 libs/gopherbuzz@0.2.0
```

These modules version independently, each on its own cadence, because Go already
gives each its own tag namespace. Deriving every tag from one number would chain
a library's public API to the root's release schedule for no reason.

## The order it enforces

Releasing the root together with a nested module has an order that is easy to get
wrong by hand, so the target does it for you:

1. **Nested modules are tagged first** (`libs/<name>/vX.Y.Z`). Each has to be
   independently `go get`-able before anything can require it at a real version.
2. **The root `go.mod` is rewritten**, repointing each `require .../libs/<name>`
   from `v0.0.0` to the version just tagged.
3. **The root is tagged last** (`vX.Y.Z`), so its tag captures that rewrite.

Get this backwards and the root tag ships a `go.mod` requiring versions that do
not exist, which makes `go get github.com/egladman/magus` fail for everyone
outside the repo.

The `replace` directives in `go.mod` are left exactly as they are. `replace` is
never transitive, so no downstream consumer sees them; they are the standard
pattern for local development inside one repo, and `go.mod` carries a comment
saying so.

A module the root's `go.mod` does not require is tag-only, and step 2 skips it.
That covers a module written in another language, and equally a Go module nothing
in the root imports: `libs/orphanator` is compiled into a golangci-lint binary
rather than imported, so the root has no require line to repoint.

## What it refuses

Every check runs before any tag is created, so a rejected release cannot leave
half the modules tagged:

- **A version that is not a legal next one.** It must be the patch, minor, or
  major successor to the current version. A prerelease or `+metadata` build of one
  of those is fine.
- **A tag that already exists.** Checked across every named module first.
- **An unknown module key**, so a typo cannot quietly release nothing while
  reporting success.
- **A major bump Go could not resolve.** From v2 onward Go requires the module
  path to end in `/vN`, and `go get` resolves the path rather than the tag, so a
  `v2.0.0` tag beside a `go.mod` still declaring the v1 path is uninstallable for
  every consumer. Renaming a module means rewriting every import of it, which
  belongs in its own commit, so the release is refused rather than fixed.
- **A dirty tree**, under `cd`: `release: tree is dirty; commit before releasing`.
  The dry run warns about this rather than failing, so you can rehearse from a
  working tree.

## Pushing

Nothing above pushes. Review the tags, then push when you mean it:

```bash
git push origin v0.4.0
```

### Prereleases and the image channel

`release` has no channel charm. Cutting a version tag has only one channel, so
there would be no alternative for the word to distinguish. Container images do
have channels, and they are chosen by `image-build`:

```bash
magus run image-build:cd,unstable   # version tag only, no floating tag
magus run image-build:cd,stable     # version tag plus latest
```

This matters when you push a prerelease root tag. `release.yaml` hardcodes
`image-build:cd,stable`, and the stable channel is the one `latest` follows, so it
rejects any version carrying a prerelease component:

```text
image: `v0.5.0-rc.1` carries the prerelease `rc.1`, so it cannot go to the stable
channel - that is the channel `latest` follows.
```

That failure is deliberate. Publishing a release candidate means editing the
workflow to `:cd,unstable` for that run, which is an explicit decision rather than
a tag-shape guess made on your behalf. Cutting and pushing the tag itself needs
nothing special; only the image job cares.

### Which tags the workflow reacts to

**Only a root `vX.Y.Z` tag triggers the release workflow.** `release.yaml` matches
`v*`, and a nested tag like `libs/orphanator/v0.1.0` does not start with `v`, so
pushing it publishes no binaries and no images. That is intended: a library tag
exists to make the module resolvable by `go get`, and it needs no artifacts.
Push it like any other tag and consumers can require it immediately.
