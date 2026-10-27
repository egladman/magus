---
title: Assemble an OCI image with magus
description: Goals, non-goals, seams, and Specs for assembling OCI Image Layouts from files magus targets already built—without Dockerfile vocabulary or BuildKit mount theatre.
tags:
  [
    oci,
    image,
    assemble,
    layout,
    layer,
    container image,
    dockerfile,
    scratch,
    multi-arch,
    sbom,
    provenance,
  ]
aliases: [setup/assemble-oci-image]
---

# Assemble an OCI image with magus

magus packages **files a target already built** into an [OCI Image Layout](https://github.com/opencontainers/image-spec/blob/main/image-layout.md).
It does not run a container to build those files. The stack of cards is layers of
filesystem diffs; invalidate a lower card and everything above must be reassembled.

This page is the stance and the seams. The Buzz module is [`image`](../../reference/buzz/image.md).
The decision record is [ADR 0018](../../decisions/0018-magus-assembles-oci-images.md).
Worked examples live under [`std/examples/image/`](../../../std/examples/image/).

## Goals

- **Teach containers by naming OCI things:** Layout, Layer, Manifest, Index, Digest, DiffID.
- **Keep Specs programmatic and typed** in Buzz/Go (`NewSpec` / `With*`, placements), not a
  stringly Dockerfile DSL.
- **Make tiny images the easy path:** scratch (or a digest-pinned base) plus explicit file
  placements, with digests and sizes you can assert on.
- **Determinism:** equal explicit inputs → byte-identical layouts (sorted entries, Unix 0 mtimes).
- **Loud failure:** bad destinations, platform mismatch, and escapes from the work root error
  with a named cause—not a silent empty path.

## Non-goals

| Not trying to | Why |
| --- | --- |
| Parse or execute Dockerfiles | Recreates Docker footguns inside a typed language (Dagger's failure mode). |
| `RUN` / package install / `ldconfig` inside the image | Build-time execution stays with a container engine or a magus **target** on the host. |
| BuildKit `RUN --mount=type=cache` / `type=ssh` verbs on `image.*` | Mounts exist to feed **in-image** build steps. magus has no such steps; caches and SSH agents belong to **targets**. |
| Silent `$HOME` / `$VAR` expansion inside Spec source paths | Call sites resolve paths with `env\home`, `path\expandUser`, `env\require`—then pass a concrete host path. |
| Scaffolding generators that invent a Spec for you | Explicit Specs beat generated Dockerfile cosplay. |
| Windows container images (yet) | Hosts may be Windows/macOS/Linux; **image** platforms are typically `linux/*`. |
| Auto-pull of a missing base | Assemble is local-only; refresh is `image\pullLayout` under the **`update`** charm. |

## Seams (where work happens)

```text
┌─────────────────────────┐
│ magus targets           │  compile, test, fetch, use Secret / SSH agent /
│ (host, sandboxed)       │  tool caches — ordinary procs, not image mounts
└───────────┬─────────────┘
            │ files on disk (work root)
            ▼
┌─────────────────────────┐
│ image\assembleLayout    │  ≤1 new Layer over an optional **local** base Layout
│ image\assembleIndex     │  multi-arch: loop layouts → Index
│ image\attachArtifacts   │  SBOM / provenance as referrers (not layers)
└───────────┬─────────────┘
            │ OCI Layout directory
            ▼
┌─────────────────────────┐
│ image\publishLayout     │  registry push; auth is Secret, never a bare password
│ image\pullLayout        │  registry fetch — only when a target runs with `:update`
└─────────────────────────┘
```

| Seam | Owns | Does not own |
| --- | --- | --- |
| **Target** | Producing bytes (binaries, static assets), private git, caches; gating pull on `:update` | OCI config, layers, manifests |
| **`libs/image` / `image` module** | Placements, config argv (`command` / `entrypoint`), digests, layout I/O | Shells, apt, BuildKit mounts, implicit network |
| **`libs/oci`** | Pull/push transport | Specs / assemble |
| **Sandbox** | Host path confinement (`filesystem.Under`, allowlists) | Image-destination POSIX rules |

**Network:** assemble and index never phone the registry. `pullLayout` and `publishLayout`
are the only registry verbs. A missing base fails naming the local path and `:update` /
`pullLayout`—there is no magus `--pull` flag; the ask is the **`update`** charm (same
grant as `go mod tidy` / `trivy-image`). See [`pull-base.buzz`](../../../std/examples/image/pull-base.buzz).

**Home and env:** use `env\home()`, `path\expandUser`, `env\require` / `env\expand` at the
call site, then pass the resolved path into a placement. Spec fields are paths and argv—not
env-name types. See [env](../../reference/buzz/env.md) and [path](../../reference/buzz/path.md).

**SSH / cache mounts:** document as target patterns (example under
[`std/examples/image/`](../../../std/examples/image/)), never as `image\withSSHMount`.

## Stack of cards

```text
base layers  →  Layer(files…)  →  config + manifest
     ↑ invalidate here              ↑ reassemble above
```

Today one `assembleLayout` adds **at most one** new Layer over a base. Multi-layer stacks are
multiple assemble steps (or a fatter base)—the receipt/trace records which Layer was added.

## Debugging (direction)

Not all of this is shipped as CLI flags yet; the intent is Delve-like **inspectability**, not a
second Docker CLI. Delve does not invent a parallel language for your program—it stops on the
artifact and lets you inspect it. Assemble should do the same: produce a Layout + receipt you
can open, not a special “debug Dockerfile” dialect.

| Need | Direction |
| --- | --- |
| What did assemble do? | Fields on every Layout: op, digests, file counts, layer, **`created`** (RFC 3339 from config, empty when omitted). Callers compute age from `created`; Layout is not a log. Strip is on `DirPlacement` — assert destinations, not prose |
| Why is this fat? | Inventory: top paths by size (advise via IMG diagnostics when over budget / empty `User`) |
| Assert in CI | Stable digests on Layout/Published; compare two layout dirs by digest without a daemon |
| “Output a file” | The **primary output is an OCI Layout directory** (and optional pushed digest). SBOM/provenance are files you `write*` then `attach`. Layout→`oci-archive` tar is optional and only if Layout dirs prove insufficient—`skopeo`/`oras` already speak Layout |
| Step through | Candidate: dump receipt as JSON Lines (`-o receipt` / template), pause-on-diagnostic for IMG codes—not a global `--debug` soup that reprints the Spec |
| External inspect | `skopeo inspect oci:<layout>`, `oras`, `dive` on an exported form—document these before inventing `magus image inspect` |

Open design (ADR 0018 D6): whether magus grows first-class `image inspect` / compare /
layout-to-tar verbs, or stays receipt + Layout + external tools. Reopen only with evidence the
directory form is not enough.

## Official-image examples

Examples are mapped from real [Docker Official Images](https://github.com/docker-library/official-images)
Dockerfiles (hello-world, busybox rootfs, nginx/httpd config overlay, redis/python
non-goals)—not invented toys. Each example quotes what the upstream Dockerfile does,
what `image\assemble*` expresses, and what stays on a target or a container engine.

See [`std/examples/image/README.md`](../../../std/examples/image/README.md).

## Related

- [Run magus from a container image](container-image.md) — consuming published magus images
- [ADR 0018](../../decisions/0018-magus-assembles-oci-images.md)
- [image module reference](../../reference/buzz/image.md)
