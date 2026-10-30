---
title: Run magus from a container image
description: Pull and run the official magus OCI images from GHCR or Docker Hub with Docker, Podman, or any OCI runtime, mount your workspace, extract the binary without running a container, read the SBOM, and verify the cosign signature.
tags:
  [
    container,
    container image,
    image,
    oci,
    docker,
    podman,
    nerdctl,
    ghcr,
    docker hub,
    registry,
    cosign,
    sbom,
    provenance,
    install,
  ]
aliases: [setup/container-image]
---

# Run magus from a container image

Official images are published to two registries:

- **`ghcr.io/egladman/magus`** on the GitHub Container Registry
- **`docker.io/egladman/magus`** on Docker Hub

Both receive the same tags from the same build, so a given tag is the same image
digest in either place. Pick whichever your environment already authenticates
against; GHCR is the primary, and the rest of this page names it for brevity.
Either way, the images are a drop-in alternative to installing the binary, which
is convenient on CI runners and in throwaway environments.

They are ordinary [OCI](https://opencontainers.org/) images with nothing
Docker-specific about them. The examples below use `docker` because it is the
most familiar, and every one of them works unchanged with `podman`, `nerdctl`, or
any other OCI runtime. Browse the published tags and digests on the
[package page](https://github.com/egladman/magus/pkgs/container/magus).

Each image runs magus as its entrypoint against `/workspace`, as a non-root user.
Mount your repository there and pass a magus command:

```sh
docker run --rm -v "$PWD":/workspace ghcr.io/egladman/magus:latest ls
docker run --rm -v "$PWD":/workspace ghcr.io/egladman/magus:latest run ci
```

The default command is `ls` (list projects and targets), so a bare
`docker run --rm -v "$PWD":/workspace ghcr.io/egladman/magus:latest` is a quick smoke
test.

## Variants

| Image                                   | Base                  | Platforms                | Notes                                                                         |
| --------------------------------------- | --------------------- | ------------------------ | ----------------------------------------------------------------------------- |
| `ghcr.io/egladman/magus:latest`         | distroless/static     | linux/amd64, linux/arm64 | Fully static, no libc. The default; use this unless you need something below. |
| `ghcr.io/egladman/magus:latest-dynamic` | distroless/cc (glibc) | linux/amd64              | Runs on glibc, so Buzz FFI works: a script can load a shared library.         |

Use `latest` unless you know you need the other one. The difference is Buzz's FFI. It
loads shared libraries through the system's dynamic loader, and distroless/static has
none, so the static binary is built without FFI and a script that calls it fails with
BZZ2003. The dynamic image keeps FFI on the glibc distroless/cc carries. Everything else
works the same in both, `magus watch` and `fs\watch` included: they read inotify through
system calls and need no extra package.

## Tags

- `latest` / `latest-dynamic` follows the most recent release.
- `<version>` / `<version>-dynamic` pins one release, for example `__MAGUS_VERSION__` or
  `__MAGUS_VERSION__-dynamic`. Pin a version in CI so a run stays reproducible.

```sh
# The hosted docs substitute the latest release tag below; on GitHub it reads
# literally - get the real value from https://github.com/egladman/magus/releases
docker pull ghcr.io/egladman/magus:__MAGUS_VERSION__
```

### Per-commit images

Every merge to `main` also publishes a static image tagged with that commit's short
hash, so you can run an exact commit without waiting for a release:

```sh
docker run --rm -v "$PWD":/workspace ghcr.io/egladman/magus:a1b2c3d ls
```

These are **not releases**. They are GHCR-only (never Docker Hub), static-only (no
`-dynamic` variant), have no moving tag to follow, are not pruned on any schedule, and
carry no compatibility promise. They are signed, but by `cd.yaml` rather than the
release workflow, so the verify command below deliberately rejects them; see
[Verify the signature](#verify-the-signature).

Two images, two channels, and one charm tells them apart: `magus run image-build:cd`
publishes to the per-commit channel, `magus run image-build:cd,stable` to the stable
one. Neither word is decoration: `cd` is what makes a build publish at all, and
`stable` is which stream it lands in.

## Build the image yourself

```sh
magus run image-build
```

No Dockerfile and no `docker build` is involved. magus cross-compiles the binary, pulls
the digest-pinned distroless base, and assembles an OCI image layout with its `image`
module; the recipe is `hack/magusfile/image.buzz`. A local build then loads that one
platform into your docker image store with `docker load`, tags it `magus:<commit>`, and
runs `magus version` inside it, so an image that cannot start fails the build. It
defaults to your host's architecture, so an Apple Silicon machine builds `linux/arm64`.

| Command                               | Builds                                          |
| ------------------------------------- | ----------------------------------------------- |
| `magus run image-build`               | static variant (the default), host architecture |
| `magus run image-build:arm64`         | static variant, forced `linux/arm64`            |
| `magus run image-build:amd64`         | static variant, forced `linux/amd64`            |
| `magus run image-build:dynamic,amd64` | dynamic (glibc) variant, `linux/amd64`          |

The dynamic image is built for `linux/amd64` only, so on an arm64 host it needs the
`amd64` charm; without it the build stops and names the platform. A forced architecture
compiles as fast as the host's, since Go cross-compiles, but running a foreign one
goes through your runtime's emulation and is slow.

Multi-architecture images are built only when publishing, where the result is pushed
to a registry rather than loaded locally.

## Install the binary without running a container

The static image is a single static binary on distroless/static, so the image doubles as
a distribution channel: copy the binary out and run it on the host, no container
runtime involved at run time. This is handy on a machine that already has a runtime
but no install script, and it is how several projects distribute cross-platform
tools.

```sh
id=$(docker create ghcr.io/egladman/magus:__MAGUS_VERSION__)
docker cp "$id":/magus ./magus
docker rm "$id"
./magus version
```

`podman` works the same way with `podman create` / `podman cp`. Pin a version rather
than `latest`, so what you extract is reproducible.

This only works with the static image. The binary in the `-dynamic` image is linked
against that image's glibc and does not run on an arbitrary host. What you extract is
the same static build the `_static` release archives ship.

## Verify the signature

Every pushed image digest is signed with [cosign](https://github.com/sigstore/cosign)
at release time, keyless through Sigstore OIDC, so there is no long-lived key to
leak. To confirm an image came from this project's release workflow:

```sh
cosign verify ghcr.io/egladman/magus:latest \
  --certificate-identity-regexp '^https://github.com/egladman/magus/.github/workflows/release.yaml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

**Use cosign v3 or later.** Signatures are written in the Sigstore bundle format that
v3 made the default. A v3 client verifies both formats, but a v2 client cannot read a
v3 signature and reports the image as unverified, which looks exactly like a bad
signature. Check with `cosign version` before concluding anything from a failure.

A missing or mismatched signature means the image is not an official build. Do not
run it.

A signature lives in the registry beside the image it covers, so it is pushed to
each registry separately even though the digest is identical. Verify the reference
you pulled; swap `ghcr.io` for `docker.io` above if that is where the
image came from.

The identity regexp pins the release workflow specifically, which is what makes the
command reject a [per-commit image](#per-commit-images):
those are signed too, but under `cd.yaml`, so they fail this check by design rather
than by accident. To verify one on purpose, name that workflow instead:

```sh
cosign verify ghcr.io/egladman/magus:a1b2c3d \
  --certificate-identity-regexp '^https://github.com/egladman/magus/.github/workflows/cd.yaml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Read the SBOM and provenance

Every published image index carries two OCI 1.1 referrers: an [SPDX](https://spdx.dev/)
2.3 SBOM and an in-toto statement with [SLSA](https://slsa.dev/) v1 provenance. magus
writes both when it assembles the image, names the index by digest in each, and pushes
them after it. They are separate manifests whose `subject` is the index, not
attestations inside it, so `docker buildx imagetools inspect` does not show them.

| Document   | Artifact type                  | File                     |
| ---------- | ------------------------------ | ------------------------ |
| SBOM       | `application/spdx+json`        | `sbom.spdx.json`         |
| Provenance | `application/vnd.in-toto+json` | `provenance.intoto.json` |

Any client that reads the OCI Referrers API finds them. GHCR serves that API; on a
registry without it, magus also lists them in the `sha256-<hex>` tag the distribution
spec defines as the fallback, which the same clients read. With
[ORAS](https://oras.land/):

```sh
oras discover ghcr.io/egladman/magus:__MAGUS_VERSION__
oras discover --artifact-type application/spdx+json ghcr.io/egladman/magus:__MAGUS_VERSION__
```

`discover` prints each referrer's digest. Pull one to read it:

```sh
oras pull ghcr.io/egladman/magus@sha256:<referrer digest>
```

That writes `sbom.spdx.json` or `provenance.intoto.json` to the current directory.

The provenance names the workflow that built the image as its builder, the commit as
its source, and the distroless base's digest as a dependency. The SBOM names the image
by digest and carries no file or package inventory. For the Go modules and the base's
packages, scan the image itself:

```sh
trivy image ghcr.io/egladman/magus:__MAGUS_VERSION__
```

## Next steps

- New to magus? Start with [Targets](../../concepts/targets.md) and [Spells](../../concepts/spells.md).
- Prefer a native binary? See the [install guides](../setup.md#install).
