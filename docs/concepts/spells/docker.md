---
title: docker spell
generated_from: spells/docker/spell.buzz
description: "Docker spell: image build, build-check, buildx, and hadolint Dockerfile linting."
tags: [docker, spell, container, image, hadolint, tools]
---

# docker

The `docker` spell forks the `docker` CLI (and `hadolint`) to build images and lint Dockerfiles. `docker-build-check` runs the builder's `--check` preflight without producing an image.

**Runtime name:** `docker` (source `spells/docker/`)

**Version probe (docker):** `docker --version`

**Version probe (hadolint):** `hadolint --version`

## Passing arguments to ops

Every op is invoked as `docker["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command, replacing any trailing defaults the op declares (go-test's `./...`), so passing args also states the scope. Omit it and a bare `docker["<op>"]()` keeps the defaults and forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L168) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L172) |


Working directory and environment are NOT options: they ride the context, as `docker["<op>"](ctx.withCwd("sub"))` and `docker["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise; passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## docker-build

**Command:** `docker build`

### Example

<!-- magus-run-recorder -->
```buzz
// docker-build's base command is just `docker build`, so pass the image tag and
// build context: `magus run image` forks `docker build -t app:latest .`.
import "magus";
import "magus/spell/docker";

magus\project({ "spells": [docker] });

export fun image(ctx: magus\Context, args: [str]) > void {
    docker["docker-build"](ctx, { "args": ["-t", "app:latest", "."] });
}
```

## docker-build-check

**Command:** `docker build --check`

### Example

<!-- magus-run-recorder -->
```buzz
// docker-build-check runs the builder `--check` preflight over a context without
// producing an image; pass the build context (`.`).
import "magus";
import "magus/spell/docker";

magus\project({ "spells": [docker] });

export fun image_check(ctx: magus\Context, args: [str]) > void {
    docker["docker-build-check"](ctx, { "args": ["."] });
}
```

## docker-buildx

buildx is the op that PUSHES (`--push`), so it is the one that meets a registry and the one that fails on authentication. Docker's own message is a complete diagnosis to anyone who already knows docker and an exit code to everyone else, so the hints turn it into the command to run. Registries disagree about the wording - Docker Hub says "authentication required", GHCR and most OCI registries say "unauthorized" or "denied" - so each phrasing gets an entry rather than one guess. This is what a workspace should reach for before a separate `<area>-login` target: the failure teaches the fix, so nobody has to know a convention in advance. See docs/concepts/secrets.md.

**Command:** `docker buildx build`

### Example

<!-- magus-run-recorder -->
```buzz
// docker-buildx builds with BuildKit; pass the tag and context. Add
// "--platform", "linux/amd64,linux/arm64" to the args for a multi-platform build.
import "magus";
import "magus/spell/docker";

magus\project({ "spells": [docker] });

export fun image_buildx(ctx: magus\Context, args: [str]) > void {
    docker["docker-buildx"](ctx, { "args": ["-t", "app:latest", "."] });
}
```

## docker-run

--rm is baked in: an op that leaves containers behind turns a repeated target into a disk leak. The caller supplies mounts, workdir, image and command through args.

**Command:** `docker run --rm`

## hadolint

Lints the Dockerfile, reporting in the GNU diagnostic format the tool declares in mgs_getTools, so magus reads each finding's file, line and rule rather than its prose.

**Command:** `hadolint -f gnu Dockerfile`

### Example

<!-- magus-run-recorder -->
```buzz
// hadolint lints the Dockerfile for common mistakes.
import "magus";
import "magus/spell/docker";

magus\project({ "spells": [docker] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    docker["hadolint"](ctx);
}
```

