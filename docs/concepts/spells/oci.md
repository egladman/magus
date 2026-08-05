---
title: docker spell
description: "Docker spell: image build, build-check, buildx, hadolint linting, and trivy/scout image scanning."
tags: [docker, spell, container, image, hadolint, tools]
---

# docker

The `docker` spell forks the `docker` CLI (and `hadolint`) to build images and lint Dockerfiles. `docker-build-check` runs the builder's `--check` preflight without producing an image, and `trivy`/`docker-scout` scan a built image for the `security` target.

**Runtime name:** `docker` (source `spells/docker/`)

**Version probe:** `docker --version`

**Named probes:** `docker-scout` (`docker scout version`), `hadolint` (`hadolint --version`), `trivy` (`trivy --version`) - each records UNPROBED when the tool is absent, and moves the cache key when installed.

## Passing arguments to ops

Every op is invoked as `docker["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `docker["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L187) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L191) |


Working directory and environment are NOT options: they ride the context, as `docker["<op>"](ctx.withCwd("sub"))` and `docker["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## docker-build

**Command:** `docker build`

### Example

<!-- magus-run-recorder -->
```buzz
// docker-build's base command is just `docker build`, so pass the image tag and
// build context: `magus run image` forks `docker build -t app:latest .`.
import "magus";
import "magus/spell/oci";

magus\project({ "spells": [oci] });

export fun image(ctx: magus\Context, args: [str]) > void {
    oci["docker-build"](ctx, { "args": ["-t", "app:latest", "."] });
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
import "magus/spell/oci";

magus\project({ "spells": [oci] });

export fun image_check(ctx: magus\Context, args: [str]) > void {
    oci["docker-build-check"](ctx, { "args": ["."] });
}
```

## docker-buildx

**Command:** `docker buildx build`

### Example

<!-- magus-run-recorder -->
```buzz
// docker-buildx builds with BuildKit; pass the tag and context. Add
// "--platform", "linux/amd64,linux/arm64" to the args for a multi-platform build.
import "magus";
import "magus/spell/oci";

magus\project({ "spells": [oci] });

export fun image_buildx(ctx: magus\Context, args: [str]) > void {
    oci["docker-buildx-build"](ctx, { "args": ["-t", "app:latest", "."] });
}
```

## docker-push

push and tag are building blocks, not a deploy pipeline: op args only append, so a verb this spell does not declare is a verb no magusfile can reach. What to tag and where to push stay entirely the caller's args.

**Command:** `docker push`

## docker-scout

**Command:** `docker scout cves`

### Example

<!-- magus-run-recorder -->
```buzz
// docker-scout is Docker's own image scanner (`docker scout cves`) - trivy's
// vendor-tool sibling; a workspace picks one for its `security` target. The
// image reference comes from the magusfile, and the advisory verdict tracks
// the scanner's database, so skip_cache applies the same way.
import "magus";
import "magus/spell/oci";

magus\project({ "spells": [oci], "targets": {
    "security": {"skip_cache": "reads Docker Scout's advisory database, which changes independently of this tree"},
} });

export fun security(ctx: magus\Context, args: [str]) > void {
    oci["docker-scout-cves"](ctx, {"args": ["myapp:latest"]});
}
```

## docker-tag

**Command:** `docker tag`

## hadolint

Find-fed rather than a hardcoded `Dockerfile` argument: a project with Dockerfile.prod or api.dockerfile variants lints ALL of them, and a bare `Dockerfile` that does not exist no longer fails the op. Op args append, so the old hardcoded form could never be pointed elsewhere from a magusfile. The prunes mirror the bash spell's find: vendored trees and stale agent worktrees are not this project's Dockerfiles to lint.

**Command:** `sh -c find . \( -name node_modules -o -path './.claude/worktrees' \) -prune -o \( -name 'Dockerfile' -o -name 'Dockerfile.*' -o -name '*.dockerfile' \) -print0 | xargs -0 -r hadolint`

### Example

<!-- magus-run-recorder -->
```buzz
// hadolint lints the Dockerfile for common mistakes.
import "magus";
import "magus/spell/oci";

magus\project({ "spells": [oci] });

export fun lint(ctx: magus\Context, args: [str]) > void {
    oci["hadolint"](ctx);
}
```

## trivy

trivy and docker-scout scan an image for known vulnerabilities - two mainstream tools, the magusfile picks one to compose into the canonical `security` target. Both take the image reference from the caller ({"args": ["myimage:tag"]}); neither bakes one in. Advisory verdicts track the scanner's database, not the tree, so pair the target with skip_cache (this repo's image-scan policy is the worked example).

**Command:** `trivy image`

### Example

<!-- magus-run-recorder -->
```buzz
// trivy scans a built image for known vulnerabilities, so it composes into the
// canonical `security` target. The image reference comes from the magusfile -
// the op bakes none in. The verdict tracks trivy's vulnerability database,
// which changes daily, so declare skip_cache on the target.
import "magus";
import "magus/spell/oci";

magus\project({ "spells": [oci], "targets": {
    "security": {"skip_cache": "reads trivy's vulnerability database, which changes independently of this tree"},
} });

export fun security(ctx: magus\Context, args: [str]) > void {
    oci["trivy-image"](ctx, {"args": ["myapp:latest"]});
}
```

