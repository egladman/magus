---
title: Assemble an OCI image with magus
description: Build OCI images as magus targets. Each stage, layer, platform, index and push is a target; the image module places files those targets wrote into an OCI Image Layout, and pulls and pushes layouts.
tags:
  [
    oci,
    image,
    assemble,
    layout,
    layer,
    container image,
    scratch,
    multi-arch,
    index,
    referrers,
    sbom,
    provenance,
  ]
aliases: [setup/assemble-oci-image]
---

# Assemble an OCI image with magus

The `image` module writes [OCI Image Layouts](https://github.com/opencontainers/image-spec/blob/main/image-layout.md):
a directory holding one Image Manifest or Image Index, its config and its layers. It
places files a target already wrote; it never runs a program inside an image.

The Buzz reference is [`image`](../../reference/buzz/image.md), the decision record
[ADR 0018](../../decisions/0018-magus-assembles-oci-images.md), and the worked examples
[`std/examples/image/`](https://github.com/egladman/magus/tree/main/std/examples/image/).

## The shape: everything is a target

```text
  build_amd64 ──> image_amd64 ─┐
                               ├─> image_index ──> image_push   (skip_cache)
  build_arm64 ──> image_arm64 ─┘
```

- **A stage is a target.** A compile, a codegen, an asset bundle: a target with literal
  `readsFiles` and `writesFiles` that runs the tool on the host.
- **A layer is a target.** `image\assemble` adds at most one layer. Several layers are
  several targets, each taking the previous target's layout as its `base`, so each
  caches on its own inputs: a source edit reruns the top layer, never the dependency
  layer below it.
- **A platform is a target**, and an **index** is a target that `ctx.needs` every
  platform target and calls `image\assembleIndex`. The platform targets run in
  parallel; fibers would not, since a host call blocks its fiber's thread.
- **A push is a `skip_cache` target**: it reads a credential and writes to a registry.

A tool that would edit the image filesystem (`useradd`, `update-ca-certificates`) is
replaced by a target that writes its output: `/etc/passwd` is a file Buzz writes. A
package manager inside the image (`apt`) is out of scope; keep a container engine for
that.

## Multi-stage

Two shapes, both without core changes:

- **Stages in one project.** The image target `ctx.needs` the stage and lists the stage's
  **sources** in `readsFiles`, never the stage's output. In one project, listing another
  target's output trips [MGS4010](../../reference/codes/race/MGS4010.md) and the target
  never caches; listing nothing replays a stale image after a source edit. The run test
  `TestStageInOneProjectCachesAndRekeysOnItsSource` pins both halves.
- **Stages in other projects.** A stage owned by another team or toolchain lives in its
  own project. A cross-project `ctx.needs(server.build)` finishes before the image
  target starts, so the stage's declared output is a plain input of the image target.

## Specs

```buzz
image\assemble(image\Spec{
    scratch = true,                       // or base = "<layout dir>"; exactly one
    platform = image\Platform{ os = image\OS.linux, architecture = image\Architecture.amd64 },
    files = [image\File{ source = "out/app", path = "/app", mode = fs\mode("0755") }],
    trees = [image\Tree{ source = "out/rootfs", path = "/",
        modes = image\ModePolicy.fixed, symlinks = image\SymlinkPolicy.refuse }],
    directories = [image\Directory{ path = "/tmp", mode = fs\mode("1777") }],
    config = image\Config{ user = "65532:65532", entrypoint = ["/app"] },
}, layout: "out/image/linux-amd64");
```

- Every closed set is an enum, and every enum starts with an unset case that is
  refused: an omitted platform is an error, never linux/amd64.
- `fs\mode` takes octal (`"0755"`) or ls notation (`"rwxr-xr-x"`); mode 0 is refused.
- A `Tree` places the **contents** of `source` at `path`. Include and exclude filter
  files only; a bad pattern is refused up front. `modes` is `fixed` (0644) or
  `executableBit` (0755 when the host owner-execute bit is set); there is no
  "keep host mode", which depends on the umask.
- `Config` follows the image-spec config field for field. A null field inherits the
  base's value and an empty list clears it; `env` and `labels` merge by key.
- On scratch, parents no entry places are written as root 0755. Over a base none are:
  an upper directory entry would replace the base's mode and owner.
- Over a base, the manifest carries `org.opencontainers.image.base.digest`, and
  `base.name` when the base came from `image\pull`.
- Every path resolves against the magusfile's directory and goes through the sandbox.

## Registries

- `image\pull(reference, platform:, layout:, auth:)` with a `name@sha256:<hex>`
  reference is content-addressed, so a target that pulls caches like any other. A
  tag-only reference is refused outside the `update` charm: a tag can name new bytes
  tomorrow. Docker media types are refused before anything is written.
- `image\push(layout, repository:, tags:, auth:)` uploads only blobs the registry
  lacks, eight at a time, hashing each once while it uploads, and returns `reference`,
  the `repository@digest` a signer or a deploy consumes.
- `auth` is an `image\RegistryAuth` whose password is a `Secret`, never a bare string.

## SBOM, provenance and referrers

`image\writeSbom` and `image\writeProvenance` write deterministic documents (SPDX 2.3;
in-toto Statement v1 with SLSA v1 provenance) naming the image by digest. Provenance adds
each base digest to its resolved dependencies. `image\attachReferrers` writes a **new**
layout with the image and one OCI 1.1 referrer per document, `subject` set and the empty
config descriptor; the subject layout is never edited. `image\push` pushes the referrers
after the image.

## Reading a layout back

`image\open(layout)` returns the layout's descriptor, its referrers, and for a
single-platform image its platform, config and layers. Each descriptor's
`digest.string()` is the OCI form, `sha256:<hex>`. In Go, `image.OpenLayout`,
`Layout.Manifests` and `Layout.ResolvePlatform` do the same.

## What this cannot express

| Not here                                    | Instead                                                                                     |
| ------------------------------------------- | ------------------------------------------------------------------------------------------- |
| Running a program inside the image          | A target runs the tool on the host, or on a Linux runner for cgo and native addons.          |
| A package manager inside the image          | A container engine, or a pinned base that already holds the packages.                        |
| Cache or SSH mounts for in-image steps      | There are no in-image steps. Caches and SSH agents belong to targets.                        |
| Windows images                              | Not yet: platforms are linux/amd64 and linux/arm64.                                           |

## Related

- [Run magus from a container image](container-image.md)
- [ADR 0018](../../decisions/0018-magus-assembles-oci-images.md)
