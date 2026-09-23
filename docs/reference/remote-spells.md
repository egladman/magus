---
title: Remote spells
description: Import a spell published as an OCI artifact by its registry path. magus.yaml declares the tag, magus.lock pins the manifest digest, only the update charm resolves a tag, and a pinned spell loads offline once cached.
tags: [spells, imports, remote, oci, registry, pinning, digest, lockfile, offline, harness, publish, credentials, override]
---

# Remote spells

A spell that is neither built in nor in your workspace can be imported from a container
registry by its repository path, the way a Go import path names its repository:

```buzz
import "ghcr.io/egladman/magus/spells/cursor";   // binds `cursor`
magus\harness.provider(cursor);
```

The spell versions apart from the binary: upgrading magus does not change the bytes a
workspace runs, and moving to a newer spell is a reviewed change to `magus.lock`. The same
path works wherever a spell is named: a magusfile import, the handle
`magus\harness.provider` takes, and a remote cache backend selector.

## Three kinds of import

The path alone says where a spell comes from ([ADR 0002](../decisions/0002-remote-spells-are-imported-by-registry-path.md)):

| Import                         | Kind      | Comes from                                            |
| ------------------------------ | --------- | ----------------------------------------------------- |
| `ghcr.io/team/spells/lint`     | remote    | a registry: the first element carries a dot or a port |
| `magus/spell/go`               | embedded  | the magus binary                                      |
| `spells/lint`, `./tools/drift` | workspace | a file in the workspace                               |

The import binds the path's last segment, as every Buzz import does; alias it
(`as claude`) when that segment is not a Buzz identifier, such as `claude-code`.

## Declare, lock, import

**1. Declare.** `magus.yaml` names each remote spell by that same path, with the tag it
tracks:

```yaml
spells:
  ghcr.io/egladman/magus/spells/cursor:
    tag: "1.4"
```

An import of a registry path with no entry fails to load with
[MGS1041](codes/magusfile/MGS1041.md). An entry that is not a lowercase registry path,
names both or neither of `tag` and `path`, or nests inside another remote path fails
when `magus.yaml` loads.

**2. Lock.** `magus.lock`, beside `magus.yaml`, pins the manifest digest each declared
tag named when it was last resolved. Only magus writes it, as YAML with sorted keys, and
it is committed:

```yaml
# Written by `magus spell lock`. Do not edit: change a tag in magus.yaml and run the update charm.
version: 1
spells:
  ghcr.io/egladman/magus/spells/cursor:
    tag: "1.4"
    digest: sha256:4f1c...
```

**Only the update charm resolves a tag.** The workspace's root magusfile has one target
that declares `magus.lock` as its output (this repository calls it `spell-lock`) and runs
`magus spell lock`. Run plain, it checks the lock against `magus.yaml` and verifies every
pinned digest without asking a registry what a tag means. Under `:update` it resolves
each declared tag, pulls and verifies the new artifacts, and rewrites the lock:

```sh
magus run spell-lock:update
```

Reviewing an upgrade is reviewing that diff. `ci` strips the update charm, so a gate can
never move a pin.

**3. Import.** Every other run reads the locked digest only. A declared spell whose lock
entry is missing, or was written for a different tag, fails its import with
[MGS1043](codes/magusfile/MGS1043.md). A registry or cache that serves bytes other than
the pinned ones fails with [MGS1042](codes/magusfile/MGS1042.md). Both are held against
the one import, not the whole workspace, so the target that repairs the lock still
loads. When the magusfile owning that target imports the stale spell itself, run
`magus spell lock --update` directly.

## Overrides

A workspace copy replaces a remote or embedded spell by a `path:` entry, the way Go's
`replace` does. The import never changes:

```yaml
spells:
  ghcr.io/team/spells/lint:
    path: vendor/lint     # a local copy replaces a remote spell
  magus/spell/go:
    path: spells/go       # a workspace copy replaces the embedded spell
```

The declaration is the acknowledgment. A copy that replaces an embedded spell must carry
that spell's name, and takes it over everywhere the name is used. An entry whose `path`
holds no spell, or replaces an embedded spell magus does not ship, is
[MGS1044](codes/magusfile/MGS1044.md). An override is never inferred from a file
existing: a workspace directory at a declared remote path, or a workspace spell carrying
an embedded spell's name, without an entry is [MGS1002](codes/magusfile/MGS1002.md).
An override points at a workspace directory only; replacing one registry path with
another is not supported.

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
magus spell pull ghcr.io/<owner>/<repo>/spells/cursor ./vendor/cursor   # the digest magus.lock pins
```

A bare registry path, as a magusfile imports it, pulls the digest `magus.lock` pins for
that declaration, so what lands is exactly what a load would run. A target directory must
be absent or empty. Every verb takes `-o json`.

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

Two readers cannot wait for a magusfile to choose a provider, so they resolve the
reference through the built-in environment provider: a workspace load pulling a locked
spell that is not yet cached, which happens before any magusfile runs, and
`magus spell lock`, which never loads the workspace because loading it reads the pins it
exists to repair. Warm the cache with the lock target where the environment holds no
token.

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

The verified spells are then laid out by import path under
`$XDG_CACHE_HOME/magus/spells/views/<id>/`, so `<view>/ghcr.io/team/spells/lint/spell.buzz`
is the entry of `import "ghcr.io/team/spells/lint"`. The view is named by the pins it
holds, so a changed lock names a new view and an existing one never changes.

## Resolution order

Remote spells resolve once per workspace load, before any magusfile runs, from
`magus.lock` alone: one read of the lock and no registry call for a cached digest. The
view joins the magusfile search roots after the workspace's own, and a workspace
directory at a declared remote path is refused rather than searched, so no local file,
relative to the process working directory or the workspace, can stand in for a remote
spell.

## See also

- [Spells](../concepts/spells.md)
- [Agent hosts](../guides/integrations/agents.md), whose harness spells are published this way
