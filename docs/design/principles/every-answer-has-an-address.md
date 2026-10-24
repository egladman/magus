---
title: Every answer has an address
order: 28
description: A run's output ref is derived from the hash of everything it declared, the way a Nix store path is; the same inputs name the same run on any machine.
tags: [design, principle, cache, determinism]
---

# Every answer has an address

This is the mechanism the rest of magus stands on, and I do not want it to read
as a detail. Every target run mints an output ref like `out357f0f8559ae`. It is
not a counter or a random id. It is the head of the run's cache key, and the key
is a hash of everything the run declared: its sources, its toolchain, its
project and target, the key schema version. Nix does the same for a store path.
Change one input and the address changes; keep them and the address is the same
on a laptop, on a runner, and next month.

That one property buys a lot. A cache hit is a lookup by address, so replaying
a run is as honest as its declarations. A ref in a pull request, a CI log or an
agent's report points at exactly one run, and `magus query output <ref>` prints
its full log wherever the run happened. `magus x <ref>` reproduces it. Two
machines that disagree about a build can compare two short strings before they
compare anything else. An agent can hand back a ref instead of pasting a
thousand lines, and the person checks the evidence rather than the agent's prose
about it.

The address is only as good as the declaration. A target that reads a file it
did not declare gets a key that cannot see the file, and a stale hit follows.
So under-declaring an input is the one way to make an address lie, and the
sandbox, the drift gate and `describe target --cache --inputs` exist to catch
it.

## Enforced by

- The [cache key](../../concepts/cache.md#the-cache-key) hashes the declared
  inputs in a fixed order, with `keyVersion` bumped whenever the hashed fields
  change.
- The [sandbox](../../concepts/sandbox.md) limits what a target may touch to
  what it declared.
- `magus job wait` accepts a worker's result only with an output ref bound to a
  passing run of that job's own check.

## Good

The ref is the key's head, and the key names its version
(`magus query output <ref> --identity`):

```text
ref:     out357f0f8559ae
key:     357f0f8559aece810f016bfa3ac3110b0ea9823e43976e6cca2b4796f3e2a863 (keyVersion 7)
```

## Bad

```text
Build #48213 passed. See the CI page for logs.
```

A build number says when something ran, not what ran. It cannot be recomputed
from the source, it differs on every machine, and it points at a log that a
retention policy will delete.

## See also

- [A cache is an accelerator, never a dependency](a-cache-is-an-accelerator.md)
- [Measured, not asserted](measured-not-asserted.md)
- [Cache model](../../concepts/cache.md)
