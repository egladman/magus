---
title: magus spell
generated_from: internal/cli/registry.go
description: "Pack a spell directory's tracked files as an OCI artifact with the standard provenance annotations, push it under one or more tags, pull and verify a published spell, list a repository's tags, and check or rewrite the magus.lock pins of the remote spells magus.yaml declares."
tags: [cli, magus spell, spell, publish, pull, oci, registry, digest, remote spells, credentials, magus.lock, lock, update]
---

# magus-spell

Build, publish, pull and list spells as OCI artifacts, and pin them in magus.lock

## Synopsis

**magus** spell \<build|push|pull|ls|lock\> [args] [flags]

## Description

A spell as an artifact: what one workspace publishes so another imports it
by registry path, pinned by digest in magus.lock and versioned apart from the
magus binary. Authoring a spell is magus init spell.

Subcommands (the first argument):

build    Pack \<dir\> exactly as push would and print the manifest digest the
           push would produce, without touching the network. --out writes the
           layer tar too.
  push     Pack \<dir\> as one uncompressed tar layer and push it to \<ref\>, a
           \<registry\>/\<repository\>:\<tag\>, then under each --tag with no second
           upload. Prints \<registry\>/\<repository\>@sha256:\<digest\>.
  pull     Fetch \<ref\> by tag or digest, verify the manifest and layer digests,
           and print the pinned reference and the directory holding the files:
           [\<dir\>] when given, otherwise the user cache. A bare registry path,
           as a magusfile imports it, pulls the digest magus.lock pins.
  ls       List \<registry\>/\<repository\>'s tags, following pagination.
  lock     Check that magus.lock pins every remote spell magus.yaml declares,
           for its declared tag, and verify each pinned digest; no tag is
           resolved. --update resolves each tag and rewrites magus.lock, and is
           what the update charm on the lock-owning target runs. The workspace
           is not loaded, so credentials resolve through the environment.

Only files the VCS tracks are packed, each with a fixed mode, owner and time,
and the manifest carries org.opencontainers.image.{title,source,revision,created},
created being the revision's commit time (SOURCE_DATE_EPOCH overrides), so one
commit builds to one digest on every machine. \<dir\> must hold a tracked
spell.buzz; a tracked symlink is refused.

Credentials: the spells.registries entry in magus.yaml for the reference's host
names a username and a secret reference, resolved through the workspace's
secret provider. --username overrides it and reads the password from stdin.
With neither, requests are anonymous. A new GHCR package is private until
someone makes it public.

### spell build options

**--out** *string*
: Also write the packed layer (an uncompressed tar) to this file

**--source** *string*
: The org.opencontainers.image.source URL; default: the VCS remote as https

### spell push options

**--source** *string*
: The org.opencontainers.image.source URL; default: the VCS remote as https

**--tag** *string*
: Another tag to write the same manifest under; repeatable

**--username** *string*
: The registry username; the password is then read from stdin, overriding spells.registries

### spell pull options

**--username** *string*
: The registry username; the password is then read from stdin, overriding spells.registries

### spell ls options

**--username** *string*
: The registry username; the password is then read from stdin, overriding spells.registries

### spell lock options

**--update**
: Ask the registry what each declared tag names now, and rewrite magus.lock

## Subcommands

**build**
: Pack a spell directory and print the manifest digest a push would produce

**push**
: Push a spell directory's tracked files as an OCI artifact and print its pinned reference

**pull**
: Fetch and verify a published spell into the cache or a directory

**ls**
: List a spell repository's tags

**lock**
: Check magus.lock against magus.yaml, or rewrite it with --update

## Examples

*Print the digest a push of this commit would produce*

```sh
magus spell build spells/harness/cursor
```

*Publish under a version and a floating tag*

```sh
magus spell push spells/harness/cursor ghcr.io/owner/repo/spells/cursor:v1.2.0 --tag latest
```

*Pull a published spell into a directory*

```sh
magus spell pull ghcr.io/owner/repo/spells/cursor:v1.2.0 ./vendor/cursor
```

*List a spell repository's tags*

```sh
magus spell ls ghcr.io/owner/repo/spells/cursor
```

*Pin every declared remote spell's tag in magus.lock*

```sh
magus spell lock --update
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

