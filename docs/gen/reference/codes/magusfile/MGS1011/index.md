---
title: "MGS1011: cross-project output names an unusable owner"
description: Fires at load when a target declares ctx.outputs into another project that magus cannot resolve or did not discover, so nothing would own, clean, or regenerate the file.
tags: [MGS1011, magusfile, outputs, cross-project, load, discovery]
---

# MGS1011: cross-project output names an unusable owner

A target declared an output into another project's tree, and magus cannot use the
project it named as the owner of that file.

```text
[MGS1011] workspace://producer: target "build": ctx.outputs declares an output
into "gen", which magus did not discover as a project; it needs a project marker
and must not sit under an ignored directory
```

## Why

`ctx.outputs(site.file("generated.txt"))` says two things at once: this target
produces that file, and the file lands in `site`'s tree rather than its own. The
second half is what makes it different from an ordinary output, and it is the
half that needs a real project on the other end.

magus files the glob on the **owning** project, because that is the project the
glob is relative to. That one record is what every consumer reads:

- `magus clean` deletes it, because it asks each project what lands in its tree.
- `magus watch` ignores it, so writing it does not trigger the rebuild that
  writes it again.
- The merge driver regenerates it during a conflicted merge, by looking up which
  project produces it.
- The dependency edge that makes `site` build *after* its writer hangs off it.

With no owner to file it on, all four silently do nothing. The file is still
produced and still snapshotted into the cache, so the build looks fine - it is
just a generated artifact that nothing tracks, nothing cleans, and nothing knows
how to rebuild.

Dropping it quietly is the failure mode this code exists to prevent. The
cross-project *input* side has always failed loudly on the same typo; outputs
must not be quieter, because the consequence is worse. An under-declared input
only weakens a cache key. An under-declared output means a later cache hit
replays a build that leaves the file missing.

## Two causes

**The path does not resolve.** The import names a directory that is not a path in
this workspace at all. Check the spelling of the `import "project/../<name>"`
line the alias comes from.

**The path resolves, but magus never discovered a project there.** This is the
one that surprises people, because the directory exists and the import succeeds.
A directory becomes a project by carrying a marker (`magusfile.buzz`,
`magus.yaml`, or a language marker such as `package.json`). Two things stop
discovery even when a marker is present:

- The directory sits under an **ignored directory**. `gen/`, `node_modules/`,
  `vendor/` and friends are pruned from the project walk, so a `gen/magusfile.buzz`
  is imported happily and never registered. Name the project something else.
- The marker is missing entirely - the import resolved through a plain directory.

## Resolution

Point the output at a discovered project:

```text
magus ls
```

If the intended owner is not in that list, it is not a project yet. Add a marker
to it, or move it out from under an ignored directory.

If you did not mean to write into another project at all, drop the alias and
declare an ordinary same-project output:

```buzz
ctx.outputs("dist/**");
```

## See also

- [MGS1012](MGS1012.md) - the same declaration forming a dependency cycle
- [MGS1013](MGS1013.md) - the glob half of a cross-project output escaping its owner
