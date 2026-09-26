---
title: sandbox diagnostics
page_type: overview
description: Landing page for the sandbox that confines magus spells to the workspace, plus its MGS2xxx diagnostics for denied reads, writes, execs, and env leaks.
tags:
  [
    sandbox,
    diagnostics,
    error codes,
    MGS2xxx,
    security,
    permissions,
    landlock,
    supply chain,
  ]
---

# The sandbox

The sandbox confines magus's subprocesses and in-process spells to the
workspace plus a curated allowlist of toolchain, cache and system paths,
and replaces the child-process environment with a minimum allowlist so
secret-bearing variables do not leak to a compromised third-party spell.
The [sandbox model](../../../concepts/sandbox.md) describes it in full.

It is designed to neutralize the class of supply-chain attacks that have
appeared on npm and the VS Code marketplace, where a compromised package
or extension reads credentials from disk and exfiltrates them.

## What sandbox blocks

- Reads outside the workspace, its private temp dir, the system trees,
  the `PATH` directories and the tool caches and installs the spells declare. This
  denies `~/.aws/credentials`, `~/.vault-token`, `~/.ssh/id_rsa`,
  `~/.npmrc`, `~/.config/op`, `~/.docker/config.json`, `~/.kube/config`,
  and the rest of the usual supply-chain targets.
- Writes outside the workspace, its private temp dir and the tool
  caches. System paths are read-only, and the shared `/tmp` is not
  granted at all.
- Execs outside the workspace, the private temp dir, the system trees,
  the `PATH` directories and the toolchain installs.
- Inheritance of secret-bearing env vars in child processes:
  `AWS_*`, `GITHUB_TOKEN`, `VAULT_*`, `OP_SESSION_*`, `NPM_TOKEN`,
  `ANTHROPIC_API_KEY`, etc. By default only `HOME`, `PATH`, `USER`,
  locale vars and terminal vars pass through.
- Writes through magus's bindings to files other tools run code from
  later: `.git/hooks`, `.git/config`, `magus.yaml`, `mise.toml`,
  `.envrc`, `.claude/` and the rest the
  [sandbox model](../../../concepts/sandbox.md#control-files) lists.

Without kernel landlock, only what goes through magus's own bindings is
checked; see [MGS2005](MGS2005.md).

## What sandbox does NOT block

- **Network egress.** A compromised spell with no token in its env can
  still `curl attacker.example`.
- **In-memory secret theft from magus itself.** If magus is holding a
  secret in memory at the moment a spell runs, landlock cannot help.
- **A child writing a control file.** Landlock cannot deny a path inside
  a grant, so only magus's own bindings refuse those writes.

## Turning it on

In `magus.yaml`:

```yaml
sandbox:
  mode: best-effort
```

Or per-invocation:

```sh
MAGUS_SANDBOX=best-effort magus run build
magus --sandbox=required run build
```

`best-effort` uses kernel landlock where the host has it and falls back
to binding-level checks ([MGS2005](MGS2005.md)) where it does not.
`required` refuses that fallback, and a kernel below landlock ABI 3, and
stops with [MGS2012](MGS2012.md) instead. `off` is the default. A nested
magus inherits its parent's mode and may only strengthen it
([MGS2010](MGS2010.md)).

## Extending the allowlist

```yaml
sandbox:
  mode: best-effort
  allow:
    - path: ~/.terraform.d/plugins
      mode: rx
    - path: ~/.cache/bazel
      mode: rw
  env:
    passthrough:
      - GOPATH
      - GOCACHE
      - CARGO_HOME
      - "MISE_*"
```

`mode` is `ro` (the default), `rw`, `rx` or `rwx`; exec is never
implied. A passthrough pattern ending in `*` is a prefix match, and its
prefix must be at least three characters ending in `_`: `MISE_*` matches
everything that starts with `MISE_`. A bad entry is an error
([MGS2004](MGS2004.md)).

## Enforcement mechanism

Two layers run together:

1. **Kernel level.** On Linux 5.13 or newer, magus starts each child
   through a launcher: magus re-executed, which applies the policy's
   landlock ruleset to itself and then execs the command. The child and
   everything it starts stay confined. magus itself is never confined.
   No root required.
2. **Binding level.** magus's own `fs`, `archive`, `crypto` and `http`
   bindings, and Buzz's own `os` and `io`, check the policy before
   touching a path, and the exec binding checks the binary it starts.
   This gives a friendly `MGS2001`/`MGS2002`/`MGS2007` error and is the
   only enforcement on macOS, Windows, or older Linux kernels.

## Codes

- [MGS2001](MGS2001.md): path read denied.
- [MGS2002](MGS2002.md): path write denied.
- [MGS2003](MGS2003.md): env vars stripped from child.
- [MGS2004](MGS2004.md): a sandbox.allow or passthrough entry failed to resolve.
- [MGS2005](MGS2005.md): landlock unavailable; binding-level checks only.
- [MGS2006](MGS2006.md): likely PATH-shim manager (mise/asdf/direnv) stripped.
- [MGS2007](MGS2007.md): exec denied.
- [MGS2008](MGS2008.md): server socket withheld from sandboxed children.
- [MGS2010](MGS2010.md): a nested or forwarded run asked for a weaker sandbox mode.
- [MGS2012](MGS2012.md): the sandbox is required and the kernel cannot confine the run's children.
- [MGS3009](MGS3009.md): machine budget exhausted.
- [MGS3010](MGS3010.md): redundant gate deferred.
- [MGS3011](MGS3011.md): target exceeded its declared timeout.
- [MGS3012](MGS3012.md): invocation stalled with its project locks held.
- [MGS3013](MGS3013.md): every build slot held by a step that is itself waiting.
- [MGS3014](MGS3014.md): gate superseded by a later gate on the same tree.
- [MGS3016](MGS3016.md): a server call against a workspace that failed to load.
- [MGS3017](MGS3017.md): a server call against a workspace still loading.
- [MGS3018](MGS3018.md): a job forked with a directory as a write path.
- [MGS3019](MGS3019.md): the merge queue's status is required from another integration than its credential's.
- [MGS3020](MGS3020.md): a --preflight target failed, so the invoked target never started.
- [MGS3021](MGS3021.md): a --preflight target outside the invoked target's closure.
- [MGS3023](MGS3023.md): a pipe whose writers loop back into the run reading it.
- [MGS3024](MGS3024.md): a hook glue call that names no agent host.
- [MGS3026](MGS3026.md): a merge queue hook flag holding shell syntax rather than a command and its arguments.
- [MGS3027](MGS3027.md): the merge queue refused a validation run the base's own queue workflow did not start.
- [MGS3028](MGS3028.md): the merge queue's plan disagrees with what apply reads itself.
- [MGS3030](MGS3030.md): a magus stage upstream of this run in a pipe exited non-zero.
- [MGS3031](MGS3031.md): a job forked with a declaration claim no footprint can grade.

MGS3015 was retired in 2026-09. It refused a run when every holder of the
isolation gate looked stalled, and it read that from a record the gate did not
own, so it could not fire for a simple step and did fire for healthy composite
ones. The shape it was built for is prevented rather than detected; a hang that
escapes that prevention is caught by MGS3012. The number is not reused.
