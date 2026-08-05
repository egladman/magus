---
title: Tool identity
description: How a spell decides what its toolchain contributes to a cache key, why the host platform is keyed separately, and why a readiness probe is deliberately excluded.
tags: [cache, version, probe, readiness, platform, spells, determinism]
---

# Tool identity

magus keys a target on its inputs, and the toolchain that built them is one. This
page is the three separate questions a spell can answer about a tool, and which of
them reach the cache key.

| question | declared by | reaches the key |
| --- | --- | --- |
| what version is installed? | `mgs_getVersionProbe` | yes |
| what part of that version matters? | `mgs_getVersionKey` | it decides |
| is the tool usable right now? | `mgs_getReadinessProbes` | **no** |

## A probe's output is not a version

Every tool wraps its version in prose, and several wrap build identity around it:

```text
go version go1.26.0 linux/amd64
golangci-lint has version 2.5.0 built with go1.25.1 from ff63786c on 2025-09-21T19:04:05Z
Docker version 29.3.1, build c2be9cc
```

Keying on those strings verbatim makes the key depend on things that are not the
tool's version: the commit golangci-lint was built from, the timestamp, the Go
version it was compiled with, docker's build hash, the host's OS and arch. Two
machines running the identical tool then compute different keys and share no cache,
and a distro rebuild moves the key with no version change at all.

## Extraction is opt-in

By default the whole probe output keys the cache - what magus has always done.
Finding the version inside that output means guessing which number is the version,
and a spell asks for that guess explicitly:

```buzz
export fun mgs_getVersionKey() > VersionKey { return VersionKey{upTo = VersionComponent.patch}; }
```

`upTo` is two requests at once: extract a semver, and keep this much of it.

- `patch` keeps all three numbers and discards prerelease and build noise. It
  narrows nothing anyone reasons about, and is the right answer for a tool that pads
  its version line.
- `minor` lets patch releases share one entry.
- `major` lets a whole major share one entry.

Narrowing past `patch` is a **claim** - that the tool's output does not change across
the component you dropped - and it trades cache hits for the risk of replaying an
artifact a different version would not have produced. None of the built-in spells
narrow past `patch`.

`const` covers a tool that cannot report a version at all: the author supplies a
string and edits it by hand to invalidate.

### Why the default is not extraction

`govulncheck -version` reports the Go version first, the scanner second, and the
vulnerability database's last-modified date last. Extraction would take the Go
version and discard the database date - so a newly published CVE could not
invalidate a cached pass. It declares no version key, and keys on its whole output.

A spell author knows their tool's output. magus does not.

## The host platform is keyed separately

Several toolchains print their platform as part of their version, so before
extraction magus keyed on it **by accident**, and only for projects that happened to
bind such a spell. Extraction strips that deliberately, so the platform is stated in
the key itself where it covers every step:

```text
os:linux
arch:amd64
```

They are separate lines because they vary independently: a container image built on
`linux/amd64` differs from `linux/arm64` by **arch** alone, while a shell test suite
differs between macOS and linux by **OS** alone. One combined switch would make a
workspace that cares about one pay for both.

Either can be left out:

```yaml
cache:
  include:
    os:
      enabled: true
    arch:
      enabled: true
```

Both default to enabled, and deliberately: being wrong that way costs cache hits,
while being wrong the other way replays a foreign artifact out of a shared cache.

A single target can override the workspace answer, using the same nesting so one
decision reads the same way wherever it is written:

```buzz
magus\project({"targets": {
    "image": {"cache": {"include": {"arch": {"enabled": false}}}},
}});
```

An axis the target does not mention inherits. A misspelled nesting level is a load
error rather than a silent inherit - the two are indistinguishable at run time, and
one of them is a cache that quietly does the wrong thing.

This is the **host** platform. The platform an artifact is built *for* travels as
`GOOS`/`GOARCH` through the environment allowlist and keys through the env lines.

## Readiness never keys the cache

A readiness probe answers whether a tool is usable *now*:

```buzz
export fun mgs_getReadinessProbes() > {str: Command} {
    return {"docker": Command{bin = "docker", args = ["info"]}};
}
```

`docker --version` is client-only - it succeeds with no daemon - so the version probe
structurally cannot detect a stopped daemon. Without readiness the op forked, docker
failed, and magus reported a build failure for a project with nothing wrong with it.
Now it fails as [MGS3004](../../reference/codes/sandbox/MGS3004.md) before forking.

The result is a **precondition, not an input**. `docker info` reports running
containers and disk usage, so mixing it into a key would invalidate every entry on
every run. This is worth stating because the neighbouring mechanism does the exact
opposite: a version key exists precisely to enter the key. The two probes look alike
and mean opposite things.

Probes are keyed by tool and resolved through an op's own `bin`, so a spell driving
both `docker` and `hadolint` gates only the former - linting a Dockerfile talks to no
daemon. `magus doctor` lists every gate without running any of them.
