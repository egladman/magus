---
title: "MGS1017: magusfile is not a spell"
description: Fires when a magusfile imports magus/spell/magusfile or lists magusfile among its spells. Magus binds that driver to every project automatically, so declaring it does nothing.
tags: [MGS1017, magusfile, spells, migration, breaking-change]
---

# MGS1017: magusfile is not a spell

A magusfile imported `magus/spell/magusfile`, or named `magusfile` in its
project's `"spells"` list. Neither has any effect: magus binds that driver to
every project it discovers.

A [spell](../../../concepts/spells.md) is a library of tool-native operations for
one toolchain - the `go` spell exposes `go-build`/`go-test`/`go-vet`, the `rust`
spell exposes `cargo-build`/`cargo-clippy`. The magusfile driver adapts no
toolchain and contributes no operations. It is what makes the targets in the file
you are writing runnable at all, which is not something an author opts into.

## Resolution

Delete the import, and drop `magusfile` from the `"spells"` list:

```diff
 import "magus";
-import "magus/spell/magusfile";
 import "magus/spell/go";

 magus\project({
-    "spells": [magusfile, go],
+    "spells": [go],
 });
```

A project whose targets all come from its magusfile binds no toolchain at all,
so it needs no list:

```diff
 magus\project({
-    "spells": [magusfile],
 });
```

Your targets keep running exactly as before. `magus ls` will stop reporting
`spell: magusfile` for the project and report the toolchain it actually binds,
or none.

## Why this is an error rather than a warning

Binding the driver became implicit because listing it was never the author's
job. That left the import and the list entry as no-ops which still taught every
reader - and every generated example - that `magusfile` was a spell like `go` or
`buf`. Failing loudly with the one-line fix ends that, rather than leaving a
declaration that looks load-bearing and is not.
