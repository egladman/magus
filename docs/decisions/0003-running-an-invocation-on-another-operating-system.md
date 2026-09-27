---
title: "ADR 0003: running an invocation on another operating system"
order: 3
description: Whether magus should learn to run an invocation on a kernel other than the host's, so a macOS checkout can reproduce a Linux-only failure (landlock, /proc, the unix socket path limit) before CI does. Decides on a prefix script in this repository only, records an execution-platform flag as considered and not planned, and lists what six reviewer archetypes found the first draft got wrong.
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
  proc socket all bind. macOS has a limit too, a few bytes shorter, so this one is about
  paths more than kernels.

CI's shards run inside `magus queue gate --sandbox=required`, the box the merge queue gates
a candidate in. When that box fails on Linux, a person on a Mac cannot run the same
command: `required` refuses before anything starts. The only way to see what CI sees today
is to push and wait.

A prototype proved the mechanism: a `linux` target in the root magusfile that runs the
podman spell's `podman-run` op with the repository mounted at its own path, the Go image
`mise.toml` pins, and magus built from the tree inside. It works on a podman machine whose
kernel has landlock. Its shape is the question.

### Why this is a hard call

Build tools at magus's layer mostly have no concept of "where this runs". Make, Turborepo,
Nx and Gradle run on the host and stop there. The tools that have one made it a large part
of their identity: Bazel's host, execution and target platforms; Pants' environments; Nix's
`system` and builders; cross-rs's container-per-target. Each spells it as one modifier on an
otherwise unchanged command and names the runtime explicitly. Each also paid for it in
concepts its users must learn, and in expectations of equivalence it then had to meet.

`docs/scope.md` states that a container gives environment reproducibility, not
hermeticity, and that magus offers no opt-in container isolation. Any answer here argues
with that page.

## Options

### A. Document the container command, build nothing

A guide carries the podman command by hand. magus learns nothing. Rejected: the command is
nine flags long and drifts from `mise.toml`; the guard's raw-tool rule denies `podman run`
to every agent and leaves it no route, so an agent wraps it in a script the guard cannot
see.

### B. A repository-local target (the prototype)

`magus run linux . -- <magus arguments>`. No engine change. Rejected in this shape: every
flaw it has comes from being a target. The nested `--` is a house dialect, the inner run's
output is one op's captured stdout, the working directory is fixed by the target, and
reaching `affected` or `queue gate` means nesting a second command line.

### B'. A prefix script in this repository (decided)

```sh
tools/on-linux magus queue gate --sandbox=required -- magus run test .
tools/on-linux magus run go::go-test . -- -run TestX
```

An executable that takes a magus command line and runs it on Linux, the way `sudo`,
`nice` and `cross` take a command. It has none of B's flaws: no nested `--`, its output is
the inner run's own stdio, it runs where the caller stands, and it reaches every verb. The
engine learns nothing.

### C. A `:linux` charm

Rejected by every reviewer. A charm patches one op's arguments and cannot change what runs;
charms key the cache, so a `:linux` run mints keys CI never mints; it means nothing on
`affected`, `queue gate`, `buzz` or `x`; and `amd64`/`arm64` already answer "build for which
architecture", a different question from "run on which kernel".

### D. An execution-platform flag relayed through a provider spell (considered, not planned)

`magus <verb> --on linux ...`: the engine relays the whole invocation to a provider spell
whose op returns the container command as data. Recorded here so a later proposal argues
with this page rather than starting over. It is not planned, and it is not scheduled by
any trigger in this ADR; see "If D is ever proposed" below.

## Decision

1. **B' now, in this repository only.** `tools/on-linux` is this repository's
   development tool, documented in a contributor guide, not a magus feature. `docs/scope.md`
   does not change. The prototype target is deleted when the script lands.
2. **D is considered and not planned.** Any proposal to put a platform concept in magus
   itself is its own ADR, and must meet the preconditions below first.
3. **C and A are rejected.**

### The contract B' must hold

Every line here is a reviewer finding the first draft missed or got wrong.

- **Scope: the kernel, not CI's whole environment.** The image carries Go and nothing else
  `mise.toml` pins (node, pnpm, golangci-lint, dprint, trivy and more). So the supported uses
  are Go tests and `queue gate` over Go targets. `magus run ci` inside is out of scope and
  the guide says so, rather than failing on the first missing tool.
- **Architecture is stated, not implied.** On Apple silicon `linux` means linux/arm64, while
  every CI job runs `ubuntu-latest`, linux/amd64. A failure that only amd64 shows does not
  reproduce, and landlock under emulation is unmeasured. The first line the script prints
  names the platform, the image digest and the inner magus build.
- **The checkout is mounted read-only**, with one writable per-platform store under
  `.magus/platform/<os>-<arch>` for magus's cache and state. A read-write mount lets a
  `build` inside replace the host's darwin `./magus` with a Linux binary, lets the default
  `rw` charm rewrite the host's generated files, and writes root-owned files with no lease
  check. `queue gate` already works on a copy of HEAD, so it needs no write access to the
  tree.
- **The environment crosses only by name.** A container does not inherit the environment of
  the process that starts it; only `-e` crosses. The script passes `MAGUS_SANDBOX`,
  `MAGUS_NO_BOOTSTRAP_EXEC=1`, the cache and state locations, and the sandbox passthrough
  list explicitly, and `NO_COLOR` and `TERM` when set. Nothing else is assumed to arrive.
- **The inner magus is built from the tree inside the container**, so the inner and host
  builds describe the same sources. This is why B' works only in magus's own repository, and
  why it stays there.
- **Nothing implicit.** It never pulls an image and never starts the VM: a missing image or a
  stopped machine is an error that prints the `podman pull` or `podman machine start` line
  for a person to run. No config key, no environment variable turns it on, and there is no
  fallback from podman to another runtime or from the container to the host.
- **Streams and exits stay honest.** stdout and stderr pass through untouched; a TTY is
  allocated only when stdin, stdout and stderr are all terminals, since `-t` merges the two
  streams. A runtime failure (podman's 125 to 127, a VM that is not running) exits 69 with a
  message saying the relay failed, never the build. The inner status passes through only
  once the inner magus has started. Ctrl-C and SIGTERM leave no container behind.
- **Follow-up commands keep the platform.** The inner run prints `reproduce:` and
  `inspect:` lines that name no platform; the guide says to prefix them with
  `tools/on-linux`, and the script's last line repeats the reproduce command with the prefix.
- **No shared cache tier is written.** The image is in no cache key and the manifest compares
  only os and arch, so a laptop run must never write a tier CI reads: remote writes are off
  inside.
- **Agents are not taught it as a default.** No hint, advisory or skill suggests it except
  for a kernel-bound failure (MGS2012, a landlock-only or `/proc`-only test). On Linux it is
  pointless, so an agent that adds it everywhere costs only the Mac user.

### If D is ever proposed

A proposal must first settle, in its own ADR:

- a failure that only a Linux kernel reproduces, seen in a second workspace, not a request
  for containers;
- where a workspace that is not magus's own gets a Linux magus, and how the inner and host
  builds are compared, failing closed;
- how the relay injects and verifies the sandbox floor rather than trusting a provider;
- where outputs land, so a relayed run never writes the host's tree;
- how a per-machine runtime (podman, Docker, Colima, Apple `container`) is chosen without a
  preference knob `docs/scope.md` refuses;
- the arm64 and amd64 question.

And it ships with, in the same change: the `docs/scope.md` amendment, refusal rows for a
`:linux` charm and a default platform, a whole-invocation relay only (never a per-target or
per-op platform, which is where Bazel's cost lives), and the spelling `--on`, since
`--platform` means the target platform in Bazel and buildx.

## Consequences

- magus as a product gains nothing. This repository gains a way to reproduce its Linux-only
  failures before CI, limited to the kernel.
- The prototype target and its nested `--` go away.
- The script is house tooling the guide must keep honest: when it cannot reproduce
  something (amd64-only behavior, the non-Go toolchains), the guide says so.

## Review

Six reviewer archetypes read the first draft of this ADR. All six chose not to put a
platform concept in magus now; none backed the charm.

| Reviewer | Position | What it changed here |
|---|---|---|
| UNIX graybeard | B now as a prefix command, never a target; a separate executable if ever promoted | B' replaced B; the environment crosses only by name; the streams, exit and signal contract |
| Bazel veteran | B, then D only once its contract settles | read-only mount; whole-invocation relay only; `--on` over `--platform` |
| Dagger veteran | B now; a higher bar for D | scope is the kernel, not CI's environment; the read-write mount's damage |
| Platform engineer | B, then D only once supportable | nothing implicit; no shared cache writes; the per-machine runtime question |
| Coding agent | B, then D only on a person's or second workspace's need | hints only on kernel-bound failures; follow-up commands keep the platform |
| justfile minimalist | B only; D as considered, its own ADR | D demoted from a plan to a record |

Two claims in the first draft were wrong and are corrected above: that the sandbox floor
crosses into the container on its own, and that cache parity with CI holds. Keys match; hits
do not, because CI records linux/amd64 and a Mac runs linux/arm64.

## Open questions

1. The script's language. This repository keeps workflow logic in Buzz behind `magus buzz`,
   but B' has to pass its argument list through untouched and stream the inner stdio. It is
   Buzz if `magus buzz` can do both without a nested `--`, and a POSIX shell script
   otherwise.
2. Whether the guard needs to recognize `tools/on-linux magus ...` as the magus command it
   prefixes, so the rules that judge magus invocations still apply to it.
3. Whether a default of linux/amd64 through emulation serves better than the host's arm64,
   once landlock under emulation is measured.
</content>
</invoke>
