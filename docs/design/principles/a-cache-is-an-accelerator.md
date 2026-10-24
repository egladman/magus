---
title: A cache is an accelerator, never a dependency
order: 4
description: A cache may shorten a derivation, never be the only copy of one; switching it off changes a duration, never an answer.
tags: [design, principle, cache]
---

# A cache is an accelerator, never a dependency

Every cache magus has (the local one, the remote one shared through CI, a
published knowledge graph pulled as an OCI artifact) skips work that the
machine in front of you could do itself. That is the whole contract. Delete
the cache directory, revoke the registry credential, unplug the network, and
a clean clone still builds, still gates, still answers. It is slower. It is
not broken.

The line to hold: a cache may shorten a derivation, never be the only copy of
one. The moment a clean clone cannot proceed without fetching something, the
artifact has stopped being a cache and become an undeclared build input, and
the questions that follow are the ones no cache should raise. Who can serve
it. What happens when they do not. Whether the bytes are the ones the source
would have produced, or the ones somebody uploaded. A tool that installs
through none of the toolchains it drives, for reasons
[Scope](../../scope.md) states as a rule, does not get to quietly depend on a
registry for its own derived state either.

The failure it prevents is quiet. A cache that becomes load-bearing does not
announce the change; it works, for months, until someone clones fresh on a
plane and discovers the build was distributed all along.

## Enforced by

- The remote cache is verified by a trust set rather than trusted for being
  reachable, and an unverifiable artifact is a miss rather than a warning
  ([Remote cache](../../concepts/cache/remote.md)).
- `magus graph pull` is an optimization over `magus graph build`, never a step
  before it.
- The test for any future store: if switching it off changes an answer rather
  than a duration, it was never a cache.

## Good

```sh
magus graph pull || magus graph build
```

The pull saves time when it works; the build gives the same answer when it
does not.

## Bad

```sh
curl -fsSL https://artifacts.example.com/schema/latest.json -o gen/schema.json
magus run build .
```

The build now needs a server to be up and honest. The fetched file is an
undeclared input with no source in the tree that could reproduce it.

## See also

- [Remote cache](../../concepts/cache/remote.md)
- [Everything is a file](everything-is-a-file.md)
- [No silent defaults](../judgment/no-silent-defaults.md)
