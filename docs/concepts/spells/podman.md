---
title: podman spell
generated_from: spells/podman/spell.buzz
description: "Podman spell: image build, push, manifest assembly, and run for the podman runtime."
tags: [podman, spell, container, image, oci, buildah, tools]
---

# podman

The `podman` spell forks the `podman` CLI. It is separate from `docker` rather than a bin substitution because podman has no `buildx`: multi-platform images are `podman build --platform` plus `podman manifest`, so the two runtimes need different ops rather than the same ops with a different binary.

**Runtime name:** `podman` (source `spells/podman/`)

**Version probe (podman):** `podman --version`

## Passing arguments to ops

Every op is invoked as `podman["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command, replacing any trailing defaults the op declares (go-test's `./...`), so passing args also states the scope. Omit it and a bare `podman["<op>"]()` keeps the defaults and forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L168) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L172) |


Working directory and environment are NOT options: they ride the context, as `podman["<op>"](ctx.withCwd("sub"))` and `podman["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise; passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## podman-build

**Command:** `podman build`

## podman-manifest

Multi-platform images without buildx: build per-arch, then assemble and push a manifest list. The caller supplies the list name and members through args.

**Command:** `podman manifest`

## podman-push

podman push is its own verb; docker reaches the same place through `buildx --push`.

**Command:** `podman push`

## podman-run

--rm is baked in for the same reason the docker spell bakes it in: an op that leaves containers behind turns a repeated target into a disk leak.

**Command:** `podman run --rm`

