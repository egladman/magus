---
title: Remote spells
description: Import a spell published as an OCI artifact, pinned by its manifest digest, so it versions apart from the magus binary and loads offline once cached.
tags: [spells, imports, remote, oci, registry, pinning, digest, offline, harness, publish, credentials]
---

# Remote spells

A spell that is neither built in nor in your workspace can be imported from a container
registry, pinned by the digest of its OCI manifest:

```buzz
import "oci://ghcr.io/egladman/magus/spells/cursor@sha256:<digest>" as cursor;
magus\harness.provider(cursor);
```

The spell versions apart from the binary: upgrading magus does not change the bytes a
workspace runs, and moving to a newer spell is an edit to the magusfile. The same form
works wherever a spell is named: a magusfile import, the handle `magus\harness.provider`
takes, and a remote cache backend selector.

## The form

```text
oci://<registry>/<repository>[:<tag>]@sha256:<digest>
```

The digest is required. A registry is content-addressed, so the manifest digest names one
artifact forever, while a tag can be moved to another. A reference without a digest fails
to load with [MGS1041](codes/magusfile/MGS1041.md). A tag beside the digest is allowed
and ignored for the pull. A registry that serves a manifest or layer whose bytes do not
hash to what the pin names fails with [MGS1042](codes/magusfile/MGS1042.md). Both are
errors: nothing is cached and the magusfile does not load.

The import must be aliased (`as <name>`): the alias is the handle you pass on.

## Publish your own spell

A spell is a directory holding a `spell.buzz`, committed to a repository. Four verbs
take it from there to a registry and back.

**1. Build.** Pack it exactly as a push would, and print the manifest digest the push
will produce. Nothing touches the network:

```sh
magus spell build spells/cursor
# sha256:4f1c...
```

The layer holds only the files your VCS tracks, sorted by path with a fixed mode, owner
and time; an untracked or ignored file never reaches it, and a tracked symlink or other
special file is refused. The manifest carries the standard OCI annotations:

- `org.opencontainers.image.title`: the directory's name.
- `org.opencontainers.image.source`: the VCS remote as an https URL with any credentials
  dropped; `--source` overrides it.
- `org.opencontainers.image.revision`: the checked-out revision.
- `org.opencontainers.image.created`: that revision's commit time in UTC;
  `SOURCE_DATE_EPOCH` overrides it.

`created` is the commit time and never the time of the build, so one commit builds to
one digest on every machine and on every day. A CI job can run `build` on a checkout and
compare the digest against a published pin. `--out spell.tar` also writes the layer.

**2. Push.** Publish under a tag, and under any number of `--tag`s beside it:

```sh
magus spell push spells/cursor ghcr.io/<owner>/<repo>/spells/cursor:v1.2.0 --tag latest
# ghcr.io/<owner>/<repo>/spells/cursor@sha256:4f1c...
```

Each blob uploads once; every extra tag is one more PUT of the same manifest. It prints
the pinned reference, which names that manifest forever while the tags can move. A
newly created GHCR package is private until someone makes it public, and an anonymous
pull cannot read it until then.

**3. List.** See what a repository holds:

```sh
magus spell ls ghcr.io/<owner>/<repo>/spells/cursor
```

**4. Pull.** Fetch by tag or digest, verify the manifest and the layer, and print the
pinned reference followed by where the files are:

```sh
magus spell pull ghcr.io/<owner>/<repo>/spells/cursor:v1.2.0            # into the cache
magus spell pull ghcr.io/<owner>/<repo>/spells/cursor@sha256:4f1c... ./vendor/cursor
```

A target directory must be absent or empty. Every verb takes `-o json`.

This repository publishes its spells with the `spell-publish` target. It declares the
spell sources as inputs, so a change to one selects it; without the `cd` charm it only
builds and prints each digest, and under `cd` it pushes.

## Credentials

A registry credential is a [secret](../concepts/secrets.md) reference, declared per host
in `magus.yaml`:

```yaml
spells:
  registries:
    - host: ghcr.io
      username: ci
      password: GITHUB_TOKEN
```

`password` is a reference, never the value. It resolves through the workspace's selected
[secret provider](../concepts/secrets/providers.md) exactly as `magus\secret.read`
resolves one, and the value is redacted from everything magus writes. Under the built-in
environment provider it names an environment variable; under a provider spell it is that
provider's own path (`Private/ghcr/token` for 1Password). The workspace is loaded to
reach its provider only when a verb needs the credential.

The entry applies to every verb, pull and ls included, so a private repository reads the
same way it is written. A host with no entry is reached anonymously. An entry whose host
is not a bare lowercase `host[:port]`, whose username or password is empty, or whose host
repeats another's fails when `magus.yaml` loads.

For GHCR in GitHub Actions, the job's `GITHUB_TOKEN` is the password and any username
works. Grant the job `packages: write` to push, and pass the token into the step:

```yaml
permissions:
  packages: write
steps:
  - run: magus spell push spells/cursor ghcr.io/<owner>/<repo>/spells/cursor:${{ github.sha }}
    env:
      GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

`--username <user>` overrides the entry for one invocation and reads the password from
standard input, the way `docker login --password-stdin` does.

magus does not read `~/.docker/config.json` or run Docker credential helpers. To reuse a
credential Docker already holds, hand it over through a secret reference: export it into
the environment the built-in provider reads, or write a provider spell whose
`resolve_secret` runs `docker-credential-<helper> get` and returns the `Secret` field.

## Caching and offline use

The first load pulls the manifest and the layer, verifies both, and stores them with the
extracted files under `$XDG_CACHE_HOME/magus/spells/sha256-<digest>/` (`~/.cache` when
unset). Each later magus process re-verifies that entry once before using it: the
manifest against the pin, the layer against the manifest, and the extracted files
against the layer. An entry that does not verify is replaced by a fresh pull. With
`MAGUS_OFFLINE=1` magus never pulls: an uncached reference fails, and a cached one that
does not verify fails with [MGS1042](codes/magusfile/MGS1042.md) instead of refetching.

## Resolution order

A remote reference is resolved by magus before Buzz's file search runs, so no local file,
relative to the process working directory or the workspace, can stand in for it.

## See also

- [Spells](../concepts/spells.md)
- [Agent hosts](../guides/integrations/agents.md), whose harness spells are published this way
