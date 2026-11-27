---
title: Buzz modules live in their layer
order: 44
description: A Buzz module belongs to one of five layers; a generic library imports no magus module, and a product mechanism is embedded by the package that owns it.
tags: [design, principle, buzz, layers]
---

# Buzz modules live in their layer

A bare import such as `import "fs";` promises a generic library: one a person
could use in any program. Once a module in that namespace imports a magus
module or names a magus concept, the promise is false, and every script that
imports it inherits the workspace. A hook that wants a disk sample should not
open the workspace to get one. So each Buzz module sits in exactly one of five
layers, and the layer decides where its source lives and what it may import.

- **std** holds generic libraries: fs, json, lcov, result. They import no
  `magus` module and name no magus concept. Their sources live in `std/lib/`.
- **magus/\*** holds product mechanisms: `magus/diagram`, `magus/reclaimdisk`,
  `magus/diskwatch`, `magus/modeltiers`. Each is embedded from the package that
  owns it, the way `libs/diagram` ships `magus/diagram`, and never from
  `std/lib/`.
- **spells** hold toolchain knowledge: how a go, node or buf project is built.
- **library** holds importable policy: `libs/analyzer` and `libs/guardrule`. Each is
  a nested Go module that also holds its Buzz sources. Both import std and `magus`
  modules only, and nothing under `hack/`, `magusfiles/` or another `libs/` path.
  `libs/guardrule` may also import `libs/analyzer`. A workspace imports a library
  by path or pins it by registry path.
- **magusfile and hack/** hold this repository's policy: what it trusts, what
  it gates, how it reclaims its own disk.

A Go host module that reads the run, such as `emit`, which reads the resolved spells and
the lease, is registered beside the package that owns that state, as `splice` is, and
refuses rather than answers empty when the run's context is absent.

## Enforced by

- `TestStdLibRefusesMagus` in `std/lib_test.go` fails when a source under
  `std/lib/` imports a `magus` module, or when a `magus/` path is registered from
  a `std/lib/` source.
- `TestDiskModulesRegisterAtTheirMagusPaths` in `std/lib_test.go` holds the
  disk modules at their `magus/` paths, so the hook's import is the one the
  layer names.
- Planned, not yet written: a `hack/lint` analyzer fails a `.buzz` file under
  `libs/analyzer` or `libs/guardrule` that imports a workspace path outside its
  own directory. It reads import statements, so the check is mechanical.

## Good

The disk sampler is a product mechanism, owned by the disk package and
registered at its magus path. The hook imports it by that path:

```buzz
import "magus/diskwatch";
```

## Bad

A std source that reaches into the workspace. The bare name promises a
library, and this one reads the magusfile:

```buzz
namespace fsx;

import "magus";
```

`TestStdLibRefusesMagus` refuses it: a source under `std/lib/` imports no
magus module.

## See also

- [Abstract at the right layer, or not at all](abstract-at-the-right-layer.md)
- [One source, every door](one-source-every-door.md)
- `std/lib.go`, where each module is registered
