---
title: "ADR 0003: running an invocation on another operating system"
order: 3
description: Whether magus should learn to run an invocation on a kernel other than the host's, so a macOS checkout can reproduce a Linux-only failure (landlock, /proc, the unix socket path limit) before CI does. Decides one Engine-API client reached through one user fact, a top-level --platform flag that relays the whole invocation, and symbol indexers that always run from their spell's image; records the prefix script and the provider-spell flag as considered; lists what the reviewers found and how each finding is answered.
tags: [adr, decision, platform, linux, macos, containers, podman, docker, sandbox, knowledge, scope]
status: proposed
date: 2026-09-26
supersedes: "the two earlier drafts of this page, which decided a repository-local prefix script (B') and recorded a flag relayed through a provider spell (D) as considered."
---

# ADR 0003: running an invocation on another operating system

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

A second need arrived while the first was under review. The knowledge graph's code layer
(`magus refs`, `calls`, coverage) exists only once a SCIP indexer has run, and every
indexer is a host tool a person installs first: `scip-go` and `scip-typescript` are pinned
in this repository's `mise.toml`. A graph feature that depends on installing a second tool
is a feature most workspaces never turn on.

A prototype proved the mechanism for the first need: a `linux` target in the root
magusfile running the podman spell's `podman-run` op with the repository mounted at its own
path, the Go image `mise.toml` pins, and magus built from the tree inside. It works on a
podman machine whose kernel has landlock. Its shape was the question.

### Why this is a hard call

Build tools at magus's layer mostly have no concept of "where this runs". Make, Turborepo,
Nx and Gradle run on the host and stop there. The tools that have one made it a large part
of their identity: Bazel's host, execution and target platforms; Pants' environments; Nix's
`system` and builders; cross-rs's container-per-target; and Dagger, which moved the whole
build into containers and paid for it in layers, since a newcomer must hold the SDK code,
its generated bindings, the engine, the pipeline's containers and the shell inside them
before a failure reads. Each spells the platform as one modifier on an otherwise unchanged
command and names the runtime explicitly. Each also paid for it in expectations of
equivalence it then had to meet.

`docs/scope.md` states that a container gives environment reproducibility, not
hermeticity, that magus offers no opt-in container isolation, and that magus does not
require a container runtime. This page amends the last of those, and says how much.

## Decision

1. **E, as one delivery.** An `internal/container` client (no runtime CLI, no Docker Go
   SDK); a `container.api` key honored from the user tier, the environment and the flag
   only; a top-level `--platform <os>/<arch>[/<variant>]` that relays `run`, `affected`,
   `x`, `buzz`, `queue gate` and `queue validate` and refuses every verb that reads; and
   symbol indexers that run from the image their spell names, always. `magus self fetch
   <os>/<arch>` places the inner magus for a release; `magus clean --platform [<os>/<arch>]`
   removes a platform store and everything magus labeled. No stopgap stage and no later
   phase: the prototype target is deleted in the same delivery.
2. **The indexers run from their images and from nowhere else.** The host-tool path is
   deleted, not kept behind a switch: an opt-in never gets adopted and an opt-out is a
   second backend to keep honest. The consequence is faced below: a container runtime
   becomes required for the symbol layer of the knowledge graph, and for nothing else.
3. **`docs/scope.md` changes in the same change**, as drafted under "The scope amendment".
   The refusals ledger gains the rows listed under Consequences.
4. **The measurement is part of the delivery.** No macOS runtime is named in any doc
   until Docker Desktop's kernel (landlock), the podman machine's API version, uid and
   `safe.directory` over the bind mount, named-volume ownership and cache locking over
   the VM file share are measured on real machines and published as a matrix with dates.
   Until the matrix exists, line one of a relayed run says `experimental`, which is what
   `docs/concepts/compatibility.md` asks of any feature not yet supported. The indexer
   benchmark under "The indexers" is part of the same delivery.
5. **A, B, C and D are rejected; B' is superseded.**

### The contract

Each line is a reviewer finding, kept or answered.

**The flag and the argv**

- `--platform <os>/<arch>[/<variant>]`, the Docker, buildx and OCI form. The architecture
  is required: bare `linux` is refused with a message naming the host's native
  architecture and `linux/amd64`, which is what CI runs. An architecture that is not the
  host's runs emulated, and line one says `emulated` and that landlock under emulation is
  unmeasured.
- `--platform` is a top-level flag of the must-precede kind, beside `-C` and `--config`,
  and the manpage says so; after the verb it is that verb's unknown flag (exit 2). It can
  therefore never sit inside a nested argv, and it is never read from the environment.
  `MAGUS_PLATFORM` is set BY magus for the inner magus and is not an input.
- The argv after `--platform` runs inside verbatim, with three written exceptions: the
  relay's own flags (`--platform`, `--print-request`, `--fetch`, `--container-api`) are
  stripped; `-C`, `--root` and `--config` cross unchanged when the path is under the
  mounted checkout and are refused before anything starts when it is not, naming the path;
  a working directory outside the root is refused the same way.
- `--print-request` is the relay's print mode, and it gets its own name because
  `--dry-run` after the verb belongs to the verb and would otherwise mean two things by
  position. It prints the create request as data, byte-equal to what would be sent, the
  merged environment (the image's under the request's), and a `docker run` rendering
  marked as a rendering. It connects to nothing and runs nothing.
- When `--platform` names the host's own os and arch, nothing changes and nothing is
  printed. The comparison is against `runtime.GOOS/GOARCH`, never an environment variable.
- No verb branches on `MAGUS_PLATFORM` except to prefix its `reproduce:` line. The engine
  behind every verb stays ignorant of the relay.

**The environment**

- Set by magus in the create request: `MAGUS_SANDBOX=<the host policy's mode>` as the
  floor the inner magus may strengthen and never weaken (MGS2010), which is `off` on a
  default install and is not escalated because `--platform` was typed;
  `MAGUS_CACHE_REMOTE_WRITE_ENABLED=false` as a floor the inner magus refuses to raise, not
  a default an argv flag can beat; `MAGUS_NO_BOOTSTRAP_EXEC=1`; `MAGUS_BROKER=off`, because
  the host's capacity arbitration does not reach the VM and a workspace's `broker:
  required` would otherwise refuse inside; `MAGUS_PLATFORM=<os>/<arch>`; `HOME` and `USER`
  for the volume; `TERM` and `NO_COLOR` when the caller set them.
- Told values cross by name or are an error. Every `MAGUS_*` variable in the host
  environment and every non-default value the user-tier config contributed cross under
  their generated `MAGUS_*` names, so `MAGUS_LOG_FORMAT=json` or `MAGUS_MAX_FAILURES` is
  honored inside exactly as it was outside. One whose value is a host path outside the
  mounts (`MAGUS_CACHE_DIR`, `MAGUS_CONFIG`) is refused before start, naming it. A told
  value is never dropped silently.
- `MAGUS_*` never comes from an image or its declaration. A `MAGUS_*` name in the
  workspace's image declaration is a magusfile load error; an image whose `Config.Env`
  names one is refused at inspect. The floor test asserts the merged environment, not the
  request alone.
- Nothing else crosses. No signing key, no secret, no `DOCKER_HOST`.

**The checkout and the stores**

- The checkout is mounted read-only at its own path. The platform store,
  `platform/<os>-<arch>` beside the configured cache directory (`.magus/platform/<os>-<arch>`
  by default, following `cache.dir` when it moves), is mounted read-write over `.magus`
  inside, so the
  inner magus needs no override and two platforms never share a manifest path. The VCS
  driver's repository directories are read-only at their paths; the inner magus is
  read-only at `/usr/local/bin/magus`; one labeled volume is the container user's home
  for toolchain caches and the inner job store.
- `Mounts`, never `Binds`. A source path the runtime does not share fails at create instead
  of arriving as an empty directory, and a test pins that a missing source is a refusal.
  The runtime's own wording for an unshared path maps to an MGS code that names the path
  and says to add it to the runtime's file sharing.
- A write into the tree is refused before start, never discovered as EROFS three layers
  down. Under `--platform`, `rw` and every write-back charm, including this repository's
  `default_charms: [rw]`, are refused with an MGS30xx code naming the read-only checkout;
  so is a target whose declared outputs lie outside `.magus`, and a cache replay never
  restores into the tree. `queue gate` copies HEAD into the container's temp dir and is
  unaffected; line one says `testing HEAD <sha>` so an unstaged fix is not mistaken for a
  tested one.
- `User` is the host uid:gid, always. On a Linux host the relay is a no-op and the
  indexers are the only container path, so this is what keeps `find .magus ! -user $USER`
  empty under rootful Docker.

**The inner magus**

- A release: `magus self fetch <os>/<arch>` downloads the running version's static binary
  from the release index `self update` reads, verifies it, and stores it under the user
  cache dir keyed by version. A network act named by its verb; the relay never fetches, and
  missing is a refusal printing this command. Identity is string equality, fail closed.
- A dev build, which in practice means this repository: the workspace's cross-compile
  target writes `platform/<os>-<arch>/magus` into the store, and the relay refuses unless
  running that target at the current tree would be a cache hit, since its key covers the
  sources. That is a check a person can falsify; revision equality with both-dirty
  allowed, and a patch digest stamped into the binary, were both rejected for holding a
  second identity where the cache already holds one.
- The file's GOOS and GOARCH are read from its build information and checked against
  `--platform` before create, so an exec failure inside the init never reaches a person as
  a 126 or 127 from a program they did not start.
- Building the inner magus inside the container from the mounted tree is rejected: a
  program that runs before your program is the first of Dagger's layers, and it works only
  in this repository.

**The image**

- `magus\platform.image("linux", Image{ref = "<registry>/<repository>@sha256:...", env =
  {...}})` in the root magusfile, printed by `describe`. Pins are by digest. The image is
  the environment; magus provisions nothing into it, and a target needing a tool the image
  lacks fails with MGS3003 naming the image. `describe tools --platform <p>` is the one
  read verb that accepts the flag, because its answer is a fact about the image: it runs
  the version probes inside and names what the image lacks before a run dies on the first
  missing tool.
- No pull without `--fetch`, which prints the registry host, the HTTP status on failure
  and the elapsed time. Absent is a refusal printing the same command with `--fetch`.

**The runtime**

- `container.api` is a `unix://` URL in the user config, also `MAGUS_CONTAINER_API` and
  `--container-api`, so a workflow names one on purpose (`ci.yaml` sets it for
  `ubuntu-latest`; told, not sniffed). A workspace `magus.yaml` that sets it is an error
  naming the user tier: a repository does not choose an engineer's runtime. Never
  `DOCKER_HOST`, never the context store, never a probe of known paths. `tcp://` and
  `ssh://` are refused with a message; a socket path past the platform's `sun_path` limit
  fails naming the limit and the length.
- Set and unreachable is an error naming the socket and saying a person starts the
  runtime that serves it. Unset: `--platform` refuses naming the key, and the symbol layer
  reports itself unavailable (below).
- magus sends no start or stop request to any VM. What a runtime does when its socket is
  touched is the runtime's: podman under socket activation starts its service on connect,
  and other runtimes may boot a VM. So the promise is about magus's requests, and no read
  verb connects: `doctor` reports `container.api` from config and the symbol layer's
  status from the recorded reason, and the leftover listing belongs to `clean --platform
  --dry-run`, never to plain `doctor`.
- Every container and volume magus creates carries `magus.workspace`, `magus.invocation`,
  `magus.use`, and the invoking pid and host, so `clean --platform` can tell a live relay
  in another terminal from an orphan and skips the live one. Nothing runs privileged and
  no socket is mounted into a container.

**Streams, exits and signals**

- stdout and stderr are the container's own streams demultiplexed onto the caller's
  descriptors; nothing is captured, re-encoded, truncated or decorated. The relay exits
  only after the attach stream reaches EOF and the wait has returned, so the last lines of
  a failing test are never dropped.
- A TTY is allocated only when stdin, stdout and stderr are all terminals, and raw mode is
  restored on every exit path, a panic and SIGTERM included.
- stdin crosses only when the relayed verb reads it (`affected --stdin`, `run --stdin`,
  `buzz`) or all three fds are terminals; otherwise the container's stdin is `/dev/null`,
  so `while read p; do magus --platform linux/arm64 run test "$p"; done < list` runs once
  per line. Host EOF becomes a half-close on the hijacked connection.
- Line one goes to stderr in every mode, as a JSON line under `-o json`, so stdout carries
  the inner verb's output and nothing else. At default verbosity it is one line: platform,
  `emulated` when it applies, runtime and socket, kernel, short image digest, and for
  `queue gate` the HEAD under test. Server and API version, both magus identities and
  where writes land appear under `-v` and in the JSON line.
- Refusals magus decides before touching the socket exit 2, the usage and configuration
  status: an unset key, bare `linux`, a read verb, a write-back charm, a host path outside
  the root, a missing inner magus. Never 75: `EX_TEMPFAIL` tells a retry loop to spin, and
  a missing image does not fix itself.
- Failures of the relay layer exit 71 with their own MGS30xx code: from the first
  connection until the process inside has started (socket unreachable, API too old, image
  absent, mount source missing, volume not writable), and after start when the runtime
  reports `OOMKilled` or an `Error` of its own. 71 is `EX_OSERR`, which sysexits reserves
  for "cannot fork, cannot create pipe", and a relay that could not run the process it was
  handed is that failure. It collides with nothing in magus: 2 and 3 are usage and
  preflight, 69 is what the inner magus itself exits under `broker: required` (MGS3022) so
  it cannot mark the layer, 75 is the transient class, 78 is taken by permanent machine
  refusals, and 130, 143 and 129 are the signals. Once the process inside has started its
  exit status passes through, and `-o json` carries a `layer` field naming `relay` or
  `invocation`, which is also what settles the one collision left, a magusfile calling
  `os.exit(71)`.
- The wait is registered before start, `AutoRemove` is off, the container is inspected
  after exit and deleted after that, so an OOM kill is read as an OOM kill and not as a
  test that took SIGKILL. `Init` is set so the inner magus is never PID 1.
- Signals keep magus's own convention: the first SIGINT or SIGTERM is forwarded and waited
  for with no timer, so the inner magus unwinds under `MAGUS_SHUTDOWN_GRACE`; the second
  kills and removes. SIGHUP and SIGQUIT are forwarded the same way. Exits are 130, 143 and
  129. A `kill -9` of the relay leaves the container running to completion against the
  store, labeled; the manpage says so. SIGTSTP does not stop the work inside and the
  manpage says that too.

**The cache**

- The image digest is recorded in the manifest beside os and arch, and a mismatch is a
  miss. Keys stay equal to CI's: the image is in no key, and `magus x <ref>` on an entry
  recorded elsewhere prints its platform, runtime, kernel and image digest.
- Remote writes are off inside as a floor. Remote reads stay on and miss on the digest,
  because CI's entries are minted on the runner host and carry none. Keys match; hits do
  not.
- `magus query output <ref>` returns every platform store holding the ref, each named,
  from day one; the cache page's sentence "an output ref names the same run on both" is
  rewritten in the same change.

**The indexers**

- A spell that declares a `SymbolIndexer` names its `image`, by digest, or declares none.
  The reserved `scip` op runs the spell's command in that image, on a typed `magus run
  <project>::scip` and in the daemon's background indexing alike. The mounts derive from
  the spell's sandbox declaration: the root read-only, the index directory read-write,
  each `env`-named cache (`GOMODCACHE`) read-only at its path, `GOPROXY=off` and
  `GOTOOLCHAIN=local` so a module the host never downloaded or a `go` directive newer than
  the image's Go fails as "wrong image", loudly, not as a fetch.
- The `tool:` key line is the image digest, on every laptop and in CI, so index keys are
  equal everywhere and no version probe runs in a container. CI runs the indexers from the
  same images; the `scip-*` pins leave `mise.toml`.
- What is saved is the `scip-*` binary. The toolchain a project already needs stays
  required: `scip-go` reads the host's module cache, `scip-typescript` the host's
  `node_modules`.
- With no runtime reachable, or an image absent, the symbol layer reports itself
  unavailable with the reason and the time on every read (`refs`, `calls`, coverage, the
  MCP tools that serve them) and in `doctor`, from the reason the last attempt recorded,
  never from a fresh connection. It is never silently stale, and the rest of the graph
  keeps answering.
- The daemon's background indexing starts containers, so its failures surface where a
  person reads, not only in a server log. The daemon reports its backend in `server
  status`, and a CLI whose resolved `container.api` differs from the daemon's is refused
  with the loudness of version skew, so two backends never alternate on one index.
- The daemon never pulls. An indexer image is pulled only under `--fetch` on a typed
  invocation, with the registry host, the HTTP status on failure (Docker Hub's anonymous
  limit is reached by an office behind one egress address) and the elapsed time
  printed; until then every read says the image is absent and names the command.
- Every indexer container is resource-bound, so an indexer never takes over the machine.
  The create request sets `Memory` with `MemorySwap` equal to it (no swap), `NanoCpus` and
  `PidsLimit`. The numbers are not guessed and are not a user knob: the spell declares
  them beside the indexer's image (`memory_mb`, `cpus`, `pids`), the way a target declares
  `memory_mb`, and they count against the broker's host budget like any other declared
  memory. An OOM kill is reported on every read as the indexer exceeding its declared
  bound, naming the bound and the project, never as a missing index.
- The bounds come from a benchmark that is part of the delivery: each built-in indexer
  (`scip-go`, `scip-typescript`, `scip-python`) run from its image over every project of
  this repository, with peak RSS read from the container's cgroup memory peak and CPU time
  from its cgroup accounting after each run, the largest project's peak plus a headroom
  factor (open question 7) becoming the spell's declaration. The owner's expectation, to
  be verified by the benchmark, is that indexers need little memory.
- The rust spell declares no image because none was found, so its symbol layer is absent
  until one is declared, and `doctor` says so per project.

**Agents**

- The hint sites are MGS2012 and the `x` platform lens; no skill and no MAGUS.md row
  mention `--platform`. The raw-tool deny for `docker run` and `podman run` names
  `--platform` and the `scip` op in its reason. A workspace `magus\guard.shell` deny on
  `--platform` is the documented off switch for a repository that does not want the VM
  cost.

**What magus never does**

Build an image, author a Dockerfile or Containerfile, send a start or stop request to a
VM, pull without `--fetch`, choose a runtime, detect one, read `DOCKER_HOST`, mount the
socket into a container, run privileged, add a per-target or per-op platform, add a
default platform, or expose a container API to Buzz. A magusfile that wants a step in a
container writes the podman or docker op, as today. Windows named pipes and Apple's
`container` (an XPC API, not this one) are out until one serves the Engine API or a
second transport earns its place.

### What the relay reproduces

|                                          | reproduced                                                                                                                                                                                  | not reproduced                                                                         |
| ---------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------- |
| kernel: landlock, `/proc`, seccomp       | yes; both default seccomp profiles allow the landlock syscalls, so the VM kernel is the only question                                                                                       | the exact kernel version and build of CI's runner                                      |
| paths under the checkout                 | yes, at their own paths                                                                                                                                                                     |                                                                                        |
| the toolchain                            | Go, from the declared image                                                                                                                                                                 | everything else `mise.toml` pins; `run ci` is out of scope                             |
| the unix socket path limit               | partly: the inner store sits on a VM file share at the host's path length, so `bind()` there may fail with an errno CI never sees, or succeed where CI fails; measured before it is claimed |                                                                                        |
| `HOME`, `TMPDIR`, `XDG_RUNTIME_DIR`, uid |                                                                                                                                                                                             | the home is a volume, the uid is the host's, the paths differ from CI's                |
| the filesystem                           |                                                                                                                                                                                             | virtiofs or 9p under the checkout; git rehashes a stat-dirty index on every `affected` |
| the machine                              |                                                                                                                                                                                             | CI's CPU count, memory, and linux/amd64 unless spelled out                             |

"Green on linux" means a kernel, not CI's machine or its toolchain. There are fewer
layers to author than Dagger asks for: one word on a command that does not change. There
are not fewer layers to debug when a relayed run fails, which is why line one names the
world and `--print-request` prints it.

### The scope amendment

Under "The line", the sentence "No container runtime" changes to: "No container runtime
to build, gate or answer; one is required for the symbol layer of the knowledge graph
(`refs`, `calls`, coverage), which was already gated on installing an indexer per
language and now needs one runtime instead. It is named, never detected, and never chosen
for you." Under "The container question", after the paragraph on why the dependency is
disqualifying: "That argument still holds for the orchestrator. It is accepted for the
symbol layer alone, because that layer never existed on a clone without a second tool, and
because its failure is not oblique: every read says the layer is unavailable and why. A
clean clone with no runtime builds, gates and answers everything else exactly as before."
The closing paragraph adds: "magus can also run one invocation, unchanged, on a kernel you
name with `--platform`, with your checkout mounted read-only. A relayed run shares your
checkout; it is not isolated from it."

## Alternatives

### A. Document the container command, build nothing

Rejected: the command is nine flags long and drifts from `mise.toml`; the guard's raw-tool
rule denies `podman run` to every agent and leaves it no route, so an agent wraps it in a
script the guard cannot see.

### B. A repository-local target (the prototype)

`magus run linux . -- <magus arguments>`. Rejected: every flaw it has comes from being a
target. The nested `--` is a house dialect, the inner run's output is one op's captured
stdout, the working directory is fixed by the target, and reaching `affected` or `queue
gate` means nesting a second command line.

### B'. A prefix script in this repository (considered, superseded)

`tools/on-linux magus queue gate ...`, the way `sudo` and `cross` take a command. It has
none of B's flaws and the engine learns nothing. Superseded rather than rejected: it
answers one repository's one need, shells out to one runtime's CLI, cannot serve the
indexers, and its every contract line (the environment crossing by name, the read-only
mount, the exit and stream rules) is a promise a script keeps by discipline where the
engine keeps it by construction. The reviews that shaped it shaped E too.

### C. A `:linux` charm

Rejected by every reviewer. A charm patches one op's arguments and cannot change what runs;
charms key the cache, so a `:linux` run mints keys CI never mints; it means nothing on
`affected`, `queue gate`, `buzz` or `x`; and `amd64`/`arm64` already answer "build for
which architecture", a different question from "run on which kernel".

### D. A flag relayed through a provider spell that returns a runtime CLI command (considered)

The engine asks a provider spell for the `podman run` argv and forks it. Rejected in this
shape: the provider owns the mounts and the environment, so the one property that must hold
(the sandbox floor, the read-only checkout, no shared-tier writes) holds only if the spell
remembers, and the engine must then verify a command it did not write; a per-machine
runtime becomes a repository fact, which it is not; and every runtime CLI is a second
interface with its own exit codes and its own idea of a TTY.

### E. One engine-API client, one user fact, two callers (decided)

magus speaks the Docker Engine API over a unix socket directly. Docker Desktop, podman
(`podman system service` and a podman machine), Colima, OrbStack and Rancher Desktop all
serve it. One key in the user config names the socket. Two things call the client:

```sh
magus --platform linux/arm64 queue gate --sandbox=required -- magus run test .
magus --platform linux/arm64 run go::go-test . -- -run TestPipePeer
magus -C libs/gopherbuzz --platform linux/arm64 run test
magus --platform linux/amd64 run test .              # emulated on an arm64 host; line one says so
magus --platform linux/arm64 --print-request run test .   # the create request as data; runs nothing
magus run pkg/foo::scip                              # the indexer runs from its spell's image
```

`--platform` says where this invocation runs. The `amd64` and `arm64` image charms in
this repository say what an image is built for. Two questions, two spellings, and this is
the one place the page says so.

## Consequences

- magus as a product gains the same command on another kernel when asked, and a graph
  code layer that needs no `scip-*` binary installed. It gains one dependency-free client
  and one config key, and no verb a magusfile can call.
- A container runtime is required for the symbol layer and for nothing else. That is a
  new bill, and the page above says who pays it: a laptop without a runtime loses `refs`,
  `calls` and coverage, with the reason on every read; an air-gapped workspace needs a
  mirror for the built-in images (open question 3).
- The refusals ledger records the charm, the default platform, the per-target platform,
  `DOCKER_HOST` and the context store, the implicit pull and the VM start request, the
  fall back to a host indexer, a container API in Buzz, and a host-tool indexer path.
- This repository deletes its prototype, declares its image from `mise.toml`, gains a
  target that cross-compiles the inner magus into the platform store, and drops the
  `scip-*` pins.
- CI proves the relay against the real Docker daemon on `ubuntu-latest`: an integration
  test builds the request for `linux/amd64` and runs it through the client below the
  same-platform short circuit, so the mount table, the nested store, the identity check
  and the VCS mounts run on every push. The macOS half runs on developer machines, and the
  matrix in decision 4 is where that is written down.

### Review

Four reviewer archetypes read the previous draft of E; six read the draft before it, and
the corrections they forced (the sandbox floor does not cross on its own; keys match CI's,
hits do not) stand. The four converged on one veto: the indexers rode on the relay's key,
so setting it once moved a background job into a VM with its failures in a server log. The
owner's answer is the opposite of a second opt-in: one backend, with the failures on the
read path.

| Reviewer          | Vetoes and findings                                                                                                                                                                                                                                                                                                                                                                        | Answered where                                                                                                                                                                                                                                                                                                                                                                           |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Dagger veteran    | indexers must not ride on the relay's key; `rw` failing as EROFS deep inside a tool; no runtime named until measured; the relay has no CI; `testing HEAD`; "one layer" overclaims; the keys contradiction                                                                                                                                                                                  | one backend and keys equal everywhere; write-back charms refused before start; decision 4; the ubuntu-latest integration test; line one; "fewer layers to author"; the `tool:` line is the digest for everyone                                                                                                                                                                           |
| Bazel veteran     | no per-target platform; `MAGUS_*` from an image or its declaration; the image digest gates nothing; `query output` picks one store; replay into a read-only tree; remote reads; dev-build identity by revision; one word for the axis                                                                                                                                                      | the ledger row; the load error and the inspect refusal, print mode shows the merged environment; the digest in the manifest, mismatch is a miss; every store returned; refused before start; reads stay on and miss on the digest; the cross-compile target's key; `platform` on the flag, the store, the clean flag and the declaration, said once                                      |
| Platform engineer | one key moving a daemon job into a VM; no runtime named and no experimental marker dropped before the matrix; `Binds` creating an empty checkout; remote-write-off as a floor; a `layer` field under `-o json`; patch-digest identity; orphans after `kill -9`; daemon and CLI backends disagreeing                                                                                        | failures on every read and in `doctor`; decision 4; `Mounts` only, missing source refused; the floor; the field; the target's key instead; the pid and host labels; skew refused                                                                                                                                                                                                         |
| UNIX graybeard    | the daemon never creates a container; stdin copied by default; told values dropped silently; line one on stdout under `-o json`; wait after start with `AutoRemove`; 69 meaning two things; SIGHUP; a grace timer against magus's own SIGTERM convention; "no VM start" unpromisable; `--dry-run` meaning two things by position; `-C` and `--config` naming host paths; the broker inside | the owner decided the daemon does, with every failure on the read path; stdin by verb; `MAGUS_*` and the user tier cross by name or refuse; stderr in every mode; wait before start, inspect, then delete; 71; forwarded, exit 129; first forwarded and waited, second kills; reworded to magus's requests, no read verb connects; `--print-request`; the path rules; `MAGUS_BROKER=off` |

## Open questions

1. Toolchain caches in a labeled volume (fast, invisible, removed by `clean`) or under
   the platform store on the host (visible, slow on virtiofs). Drafted as a volume;
   decided by the matrix.
2. Concurrent relays on one platform store, a person and an agent at once: whether the
   cache's cross-process locking holds over the VM file share is unmeasured. If it does
   not, the second relay per store is refused.
3. An org mirror: how a workspace substitutes its registry for a built-in spell's image
   ref, and whether `self fetch` accepts a mirror of the release index.
4. Whether one `--fetch` on a workspace-wide verb pulls every indexer image the workspace
   needs, or a pull stays per project.
5. SIGTSTP: forward as pause and unpause, or leave it documented as not stopping the
   work. Drafted as documented.
6. A per-session elapsed total when the same relayed argv runs many times, the budget
   signal an agent would read, or nothing.
7. The headroom factor over an indexer's benchmarked peak, and whether one factor serves
   memory, CPU and pids alike.

## Amendments

### 2026-09-29: B' revived while E is on hold

E is on hold indefinitely, and B' is revived as `hack/remote/on-linux.buzz`:
`magus buzz hack/remote/on-linux.buzz -- magus affected ci` runs the command, unchanged, in a local
Linux container through Podman. The need that opened this page, seeing a Linux-only failure
before CI does, has not gone away, and E is a large engine delivery whose measurements
(decision 4) have not been made. A script costs the engine nothing, so nothing is lost if E
resumes. It keeps the contract lines a script can keep: the checkout read-only at its own
path, the environment crossing only by name (`--env NAME`), the floor, the command's own
streams and exit status, 71 when the relay fails before the command starts, a landlock
kernel checked before start, and magus built on the host and run in a Chainguard image
whose git meets magus's floor (the Go image `mise.toml` pins ships an older one). It cannot
keep a digest pin, no implicit pull, a TTY, stdin, or signals waited for without a timer.
The rest of this page is unchanged.
