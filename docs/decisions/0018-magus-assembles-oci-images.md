---
title: "ADR 0018: magus assembles OCI images from built files"
order: 18
description: >-
  Whether magus should produce the container images a Dockerfile builds today. This record has magus assemble an OCI image layout from files a target already built, in Go, exposed to Buzz as the generated std module image with recipes written in Buzz. It is not Dockerfile support: no parser, no RUN, no container execution, no package installation, no runtime detection. It fixes the scope limits, the determinism rules, the timestamp policy at Unix 0, the loud-failure rules and a registry transport that extends internal/oci, and leaves SBOM and provenance and the dynamic image open.
tags: [adr, decision, images, oci, containers, buzz, std, release, determinism]
status: proposed
date: 2026-10-23
---

# ADR 0018: magus assembles OCI images from built files

## Context

The release path builds the published image with Docker. `Dockerfile.static` compiles the
binary in a `golang:1.26` stage, then copies it onto `gcr.io/distroless/static:nonroot`, a
mutable tag. `publish_variant` in `magusfile.buzz` drives `docker buildx build` with
`--sbom=true` and `--provenance=mode=max`, then signs `repo@digest` with cosign. Two things
follow from that:

- The compile inside the image repeats a build magus already ran. The release archives
  cross-compile the same binary through `release_build`, so the image carries a second,
  separately configured build of one program.
- The base is a tag, so two builds of one commit can differ, and the release needs a Docker
  daemon that magus neither models nor caches.

What the image needs is small: take a base, add a few files a target built, set an
entrypoint and a few config fields, write the result. None of that executes anything. It is
a file transform over bytes, and a transform over bytes is something magus can run in the
sandbox, cache by its inputs and reproduce.

[ADR 0003](0003-running-an-invocation-on-another-operating-system.md) lists "build an image"
and "author a Dockerfile" under what magus never does. That line was written about running
an invocation inside a container, where building an image meant driving a runtime. Assembly
needs no runtime, so the line is narrowed by an amendment on that page rather than
contradicted here.

## Decision

### magus assembles an image layout from files a target built

The Go package `internal/image` reads an optional base from an OCI image layout on disk,
adds at most one new layer holding the files and symlinks a recipe lists, rewrites the
image config, and writes a new OCI image layout. A separate, explicit step publishes a
layout to a registry. `Index` joins single-platform layouts into one image index.

| Item                                                                                           | State    |
| ---------------------------------------------------------------------------------------------- | -------- |
| `internal/image`: layer writer, config and manifest rewrite, layout read and write, `Index`    | proposed |
| The std `image` module, trampolines generated like `archive`                                   | proposed |
| Our own static image recipe, a Buzz file under `hack/magusfile/` that `magusfile.buzz` imports | proposed |
| Registry `Pull` and `Publish` over `internal/oci`, no go-containerregistry                     | proposed |
| The release path publishes the layout instead of running `docker buildx`                       | proposed |

<!--diagram:oci-assembly-->

### Go owns the rules, Buzz owns the recipe

The rules, the bytes and the transfer live in Go. Buzz reaches them through the generated
bindings: a new file under `std/` declares a `std.Module` named `image`, and `magus-utils` generates
the trampolines the way it does for `archive`. A recipe is a Buzz function that returns what
to write. Go callers inside magus use `internal/image` with typed structs, through the
exported `std.Image*` functions. The module is not added to the public Go SDK; that needs
its own decision.

### This is not `Dockerfile` support

Nothing here parses, accepts, translates or emulates a Dockerfile or a Containerfile. There
is no instruction mapping, no build context, no `RUN`, no `withExec`, no container
execution, no package installation and no runtime detection. A user with a Dockerfile keeps
using their container engine. This record reopens none of ADR 0003's refusals: magus still
runs no container, adds no container API to Buzz, and neither reads nor detects a runtime.

### Scope limits

- Platforms are `linux/amd64` and `linux/arm64` only. Windows images are out.
- One new layer per write. There is no control over layer splitting.
- No whiteouts and no deletion: a recipe cannot remove or replace a file a base layer holds.
- Sources are regular files. A directory, symlink, device, socket or FIFO source is an
  error.
- A base is named by digest only. Pulling or building from a mutable tag is refused.

### What this API cannot express

This list stays current with the module. Each item is an image magus does not produce, and
a user needing one keeps the container engine:

- An image that needs a command run at build time: compiling inside the image,
  `ldconfig`, `update-ca-certificates`.
- An image that installs distro packages, such as the dynamic variant's `inotify-tools`.
- An image that removes or replaces content of a base layer.
- A multi-stage image whose stages execute. Our stages are magus targets.

### Determinism, digest-pinned bases and loud failure

- Equal explicit inputs give byte-identical layouts: the same blobs, the same `index.json`
  and the same digests, independent of host path, file creation order, host mtimes, umask
  and `GOMAXPROCS`. The compiler's own output is outside this claim.
- Layer entries are sorted by destination. Gzip uses the standard library at a fixed
  level with no name and no mtime. Timestamps are fixed at Unix 0, as decided below.
- A base is a layout read from disk and verified: every blob is checked against its digest
  and size before use. Base layers, config and manifest bytes are reused unchanged. A
  base's platform that differs from the requested one is an error, and magus never
  relabels.
- Failure is loud and names the method: errors read `image.<method>: <what>: <detail>`.
  Invalid platforms, relative or unclean destinations, duplicate destinations, tag
  references and unsupported source types are errors that name the accepted set. There is
  no fallback and no default that substitutes for a missing input. A write that fails
  leaves the destination untouched.
- Every read and write goes through the sandbox checks, and every network call refuses
  when `MAGUS_OFFLINE` is set. Registry credentials are `secret.Value`, revealed only into
  the request header.

### The registry transport extends internal/oci

Decided by Eli on 2026-10-23. The registry transport extends `internal/oci` to carry image
manifests, indexes and blobs, and fixes its redirect guard and offline check. The OCI
descriptor, manifest, index and digest types are already direct dependencies.
`go-containerregistry` is not adopted: its `pkg/authn` imports `docker/cli/cli/config`, and
its `pkg/v1/remote` needs `authn`, so adopting it adds four modules: go-containerregistry,
docker/cli, docker-credential-helpers and logrus. A third, separate client is not an option.

### The timestamp policy is fixed at Unix 0

Decided by Eli on 2026-10-23. Every layer entry has mtime Unix 0. The gzip header has mtime
0 and no name. The config `created` field is copied from the base unless the recipe sets
one. History entries carry no `created`. A recipe that sets `created` to a commit time puts
that time into the target key through its input.

### Doctrine checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. A tag, an unsupported
  platform or a mismatched base fails with the accepted set named, and nothing is
  relabeled or substituted.
- [A record of refusals](../design/refusals.md): relied on. ADR 0003's
  refusal to run containers and expose a container API stands. The ledger rows for this
  record are added separately.
- The determinism rules follow the repository's reproducibility stance: output is a
  function of declared inputs.

## Alternatives

### A. Keep `docker` and `buildx` for the release image

Lost: it keeps a second build of the binary inside the image, keeps a mutable base tag, and
needs a daemon magus neither caches nor reproduces. Reopen if assembly cannot meet the
cannot-express list for the images the project must publish.

### B. Parse and execute `Dockerfile` files

Lost, and refused as a direction, not only for cost. It needs a parser, an instruction
mapping and container execution for `RUN`, which ADR 0003 refused to expose, and it makes
magus a build engine for another tool's language. Reopen only with new evidence about a
need assembly cannot meet, and with Eli's decision.

### C. Shell out to another image builder from a target

Lost for the release image: it adds a tool dependency and a non-reproducible step where a
deterministic transform is enough. A user may still write such an op in their own
magusfile, as ADR 0003 says for containers.

### D. Implement the recipe layer in Go only, with no Buzz module

Lost: recipes belong in magusfiles, where a target's declared outputs feed them, so the
cache key covers the image's inputs. The module is the narrow seam that lets Buzz name
files and settings while Go owns the bytes.

## Consequences

- The release image stops depending on a Docker daemon and on a second compile of the
  binary, once the release path moves to the layout publish step. Until then the
  Dockerfiles stay.
- The `image` module adds a documented Buzz API and generated files under
  `internal/interp/bindings/gen/`, `std/gen/std-api.lock` and the reference docs, which are
  regenerated, not hand-edited.
- Images that need build-time commands or packages remain outside magus, and the list
  above is the place that says so.
- Our own static image gets the base pinned by digest, which the Dockerfile's
  `distroless/static:nonroot` tag does not give today.

## Open questions

1. **SBOM and provenance without buildx (D4).** Recommended: trivy generates an SPDX SBOM
   per platform image from the layout, and `cosign attest` binds it and a SLSA provenance
   statement to the final digest. The alternative is `actions/attest-build-provenance` for
   provenance. A design is needed before code. Not decided.
2. **The dynamic image (D5).** The cgo variant with `inotify-tools` cannot be assembled
   without a package artifact or a runtime. Options: drop it, ship a statically linked
   watcher instead, or keep its Dockerfile until a design exists. Recommended: decide after
   the static cutover, with download numbers. Not decided.
