---
title: "ADR 0003: running an invocation on another operating system"
order: 3
description: Whether magus should learn to run an invocation on a kernel other than the host's, so a macOS checkout can reproduce a Linux-only failure (landlock, /proc, the unix socket path limit) before CI does. Weighs a repository-local development target against an execution-platform flag relayed through a provider spell, records the benefits and the responsibility each takes on, and the conditions under which the smaller one is promoted to the larger.
tags: [adr, decision, platform, linux, macos, containers, podman, sandbox, providers, scope]
---

# ADR 0003: running an invocation on another operating system

- **Status:** Proposed
- **Date:** 2026-09-26

## Context

Some of magus's behavior exists on one kernel only:

- **landlock**, the kernel layer of the sandbox, is Linux 5.13 and later. On macOS
  `best-effort` runs the binding layer alone and `required` refuses (MGS2012).
- **`/proc`**, which `internal/sys/pipepeer` reads to prove a pipe's peers, and which the
  root `test` target is granted.
- **the 108-byte `sun_path` limit** on unix sockets, which the daemon, the broker and the
  proc socket all bind.

CI's shards now run inside `magus queue gate --sandbox=required`, the box the merge queue
gates a candidate in. When that box fails on Linux, a person on a Mac cannot run the same
command: `required` refuses before anything starts. Today the only way to see what CI sees
is to push and wait for CI.

A prototype proved the mechanism. It is a `linux` target in the root magusfile that runs
the podman spell's `podman-run` op with the repository mounted at its own path, the Go
image `mise.toml` pins, named volumes for the Go caches, magus's cache kept out of the
host's `.magus/`, `MAGUS_NO_BOOTSTRAP_EXEC=1` so the host's darwin `./magus` is not
exec'd, and magus built from the tree inside the container. It works on a podman machine
whose kernel has landlock.

### Why this is a hard call

Build tools at magus's layer mostly have no concept of "where this runs". Make, Turborepo,
Nx and Gradle run on the host and stop there. The tools that do have one made it a large
part of their identity:

| Tool | Concept | What it cost them |
|---|---|---|
| Bazel | host, execution and target platforms; toolchains resolved from constraints | platforms and toolchain resolution are among its hardest concepts to learn |
| Pants | environments: a target can run its processes in a Docker or remote environment | an environment is a target type with its own configuration surface |
| Nix | a derivation's `system`; builds run locally only when it matches, else on declared builders | builders are explicit configuration, never inferred |
| cross-rs | `cross build --target X` is `cargo build --target X` run in a container | a separate binary, not a cargo feature |
| act | reproduces the GitHub runner locally in Docker | reproduces the whole runner, not a kernel |

The pattern across them: every mature tool spells it as one modifier on an otherwise
unchanged command, names the runtime explicitly, keeps "where it runs" separate from "what
it builds for", and treats "same as the host" as "run here". None makes the user nest one
command line inside another.

The pattern also shows the risk. Once a build tool can run somewhere else, people expect
it to be hermetic, to manage the runtime, and to make the other place feel identical. magus
states the opposite on purpose: `docs/scope.md` says a container gives environment
reproducibility, not hermeticity, and that magus offers no opt-in container isolation.

## Options

### A. Document the container command, build nothing

A guide carries the podman command by hand. magus learns nothing.

- **Benefit:** zero surface, zero responsibility; the runtime stays entirely outside.
- **Pitfall:** the command is nine flags long and must be kept in step with `mise.toml`
  by hand. The guard's raw-tool rule denies `podman run` to every agent, and there is no
  magus route to point it at. The working directory and a worktree's `.git` link are easy
  to get wrong.

### B. A repository-local development target (the prototype, cleaned up)

`magus run linux . -- <magus arguments>` in this repository's own magusfile, or in a
`tools/` module it imports. Nothing in the engine changes, and no other workspace sees it.

- **Benefit:** magus as a product takes on no responsibility. It is Buzz over an existing
  spell op, so `--dry-run` prints the podman command, the guard is satisfied, and deleting
  it is one commit. It answers the need that exists today: this repository's CI box.
- **Pitfall:** it is a wrapper target with a nested `--`, a house dialect a reader has to
  learn. Its output is one op's captured stdout, hidden on success unless `-vv`. Reaching
  `queue gate` or `affected` means nesting a second command line. Another workspace that
  wants the same thing copies it.

### C. A `:linux` charm

`magus run test:linux .`

- **Benefit:** short, and it sits beside the existing `:amd64` / `:arm64` image charms.
- **Pitfall:** rejected. A charm patches one op's arguments and cannot change what runs,
  so either every top-level target learns to re-dispatch itself or the engine grows a charm
  that breaks the charm boundary. Charms key the cache, so a `:linux` run mints keys CI
  never mints and replays nothing CI recorded. It means nothing on `affected`, `queue
  gate`, `buzz` or `x`, which are where the need is. And `amd64`/`arm64` answer "build for
  which architecture"; this answers "run on which kernel". One word must not name both.

### D. An execution-platform flag, relayed through a provider spell

One global flag names the platform an invocation executes on:

```sh
magus queue gate --platform linux --sandbox=required -- magus run ci .
magus run test --platform linux . -- -run TestX
magus affected ci --platform linux
magus queue gate --platform linux --dry-run -- magus run ci .   # prints the container command
```

**How it works.**

1. The engine parses `--platform <os[/arch]>`. If it matches the host (`runtime.GOOS` and
   `GOARCH`), nothing changes: the verb runs here, so one command line is true on a Mac and
   on the Linux runner.
2. If it does not match, the engine asks the workspace's platform provider for the command
   that runs the same argument list, minus `--platform`, on that platform. It forks that
   command with the caller's working directory, stdio and terminal passed through, and exits
   with its status.
3. The verbs that read a model (`describe`, `query`, `ls`, `explain`, `doctor`) refuse the
   flag with an error. They answer from this workspace and run nothing.

**Providers.** magus reaches systems it does not know through provider spells: a remote
cache, a CI system, a secret store and a review host each take one, wired in the magusfile
by name. A platform provider is the fifth:

```buzz
import "spells/platform/podman" as linux_on_podman;
magus\platform.provider(linux_on_podman);
```

The spell implements one op, `platform_command`, that takes the platform, the argument
list, the repository root, the working directory, and where magus should keep its cache and
state, and returns a `Command`. Because it returns data rather than running anything,
`--dry-run` and `describe` print exactly what would run. The engine knows only "relay an
invocation"; the image, the mounts, the volumes and every podman flag live in the spell.
Docker and Apple's `container` are two more spells with the same op. The engine never picks
one: the workspace wires the provider, and with none wired, `--platform` on another OS is an
error naming `magus\platform.provider`.

**What crosses the boundary, and what does not.**

- The **sandbox floor** crosses: the relay starts the container through magus's own
  process runner, which sets `MAGUS_SANDBOX` on every child, so the inner magus runs no
  weaker than the outer. `--sandbox=required` in the relayed arguments is honored inside,
  where landlock exists.
- **Cache keys** stay CI's keys; the key has no platform line. Stores do not cross: a Linux
  run writes a per-platform store (`.magus/platform/linux-arm64`), because two platforms
  sharing one store overwrite each other's manifests under the same key. `magus query output
  <ref>` on the host learns to read those stores.
- The **broker, daemon, job store, lease marker and guard** do not cross. The inner magus
  is a fresh process tree with its own state; the guard runs on the host only.

- **Benefit:** every verb keeps its meaning and gains one word. A person and an agent type
  the same thing, and the guard needs no new rule. The runtime stays a spell the workspace
  chose. Cache parity with CI holds. A second workspace gets it by wiring a provider, not by
  copying a target.
- **Pitfall:** see below.

## Responsibility D takes on

Stated plainly, because this is the part that could make it horribly good or horribly bad:

- **Expectations.** A `--platform` flag reads like a promise that the other place is
  equivalent. It is not: different filesystem performance (virtiofs on macOS), uid mapping,
  SELinux labels, network, and a different broker budget. Every gap becomes a bug report
  against magus even when it is the runtime's.
- **Scope pressure.** Once it exists, the next asks are predictable: a default platform in
  `magus.yaml`, "run CI in a container", hermetic builds, remote builders, Windows. Each is a
  preference knob or a hermeticity claim the scope page refuses today. The refusals must be
  written in the same commit, or the flag becomes the start of a container orchestrator.
- **Two meanings of platform.** Execution platform (this flag) and target platform (the
  image charms) share a word. Bazel needed three terms for this; magus would need the docs
  to hold the line every time.
- **State split.** Nothing the host knows (jobs, leases, the daemon's warm graph, the
  guard's facts) exists inside. A run that works on the host and fails inside for that reason
  will be confusing.
- **Agents.** A one-word way to reach Linux is also a one-word way for an agent to spend a
  VM's worth of time on every test run. The guard and the skills must not teach it as a
  default.
- **Security surface.** The repository is mounted into a container that runs as root by
  default, with network. The provider, not magus, decides the mounts, and a published
  provider spell would need review like any other.
- **Maintenance.** The relay is roughly 300 lines of Go plus a spell, a diagnostic code, a
  scope amendment and tests. It is small; the ongoing cost is the expectations above.

## Decision (proposed)

Decide in two steps, so the product takes on the responsibility only after the need is
shown to be real and general.

1. **Now: B, as a development tool of this repository only.** Keep the `linux` target,
   fixed so it runs in the caller's directory, keeps magus's cache in a per-platform store
   under `.magus/platform/`, and does not force `MAGUS_TEST_REQUIRE_LANDLOCK`. Document it
   in a contributor guide as this repository's tool, not a magus feature. It
   exists to fix and keep green the CI box, and the scope page does not change.
2. **Later, only if promoted: D.** Promote the target to the flag and provider contract
   when at least one of these is observed, not predicted:
   - a second workspace asks for it or copies the target;
   - agents or people keep needing it for verbs the target cannot reach cleanly
     (`affected`, `queue gate`);
   - the nested `--` or the hidden output causes a real mistake.

   Promotion lands in one change: the flag, the provider contract, the podman provider, the
   `docs/scope.md` amendment, and refusal rows for a `:linux` charm and a `magus.yaml` default
   platform. The development target is deleted in the same change.

C is rejected outright, and A is rejected because agents cannot use it.

## Consequences

- magus as a product gains nothing now; this repository gains a way to reproduce its
  Linux-only failures before CI.
- If D is adopted later, its design is recorded here, so the promotion argues with this page
  rather than starting over.
- The target is visible house dialect until then. That is accepted as the price of not
  committing the product to a platform concept on the strength of one repository's need.

## Open questions

1. Name, if D lands: `--platform` reuses the word magus already uses for `os/arch` values
   and matches podman, buildx and Apple `container`; `--on linux` is unambiguous.
2. Is "the requested platform matches the host, so run here" a guess under "told, never
   guessed"? This ADR reads it as a comparison against a fact magus already stamps into
   every cache manifest. The alternative is to always relay.
3. Where the image pin lives: a workspace-local spell reading `mise.toml`, or a declaration
   argument to `magus\platform.provider`.
4. Does Docker Desktop's kernel enable landlock? The podman machine's does; Docker's is
   unmeasured.
5. Which read verbs should refuse the flag. `doctor` ("does this workspace load on Linux")
   is arguable.
</content>
</invoke>
