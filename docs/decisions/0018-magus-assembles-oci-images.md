---
title: "ADR 0018: magus assembles OCI images from built files"
order: 18
description: >-
  Whether magus should produce the container images a Dockerfile builds today. This record has magus assemble an OCI image layout from files a target already built, in Go, exposed to Buzz as the generated std module image with Specs written in Buzz. It is not Dockerfile support: no parser, no RUN, no container execution, no package installation, no runtime detection. It fixes the scope limits, the determinism rules, the timestamp policy at Unix 0, the loud-failure rules and a registry transport that extends internal/oci, and leaves SBOM and provenance and the dynamic image open.
tags: [adr, decision, images, oci, containers, buzz, std, release, determinism]
status: proposed
date: 2026-10-23
---

# ADR 0018: magus assembles OCI images from built files

## Context

The release path builds the published image with Docker. `Dockerfile.static` compiles the
binary in a `golang:1.26` stage, then copies it onto `gcr.io/distroless/static:nonroot`, a
mutable tag. `publish_variant` in `magusfile.buzz` drives `docker buildx build` with
`--sbom=true` and `--provenance=mode=max`, then signs `repo@digest` with cosign. Two things
follow from that:

- The compile inside the image repeats a build magus already ran. The release archives
  cross-compile the same binary through `release_build`, so the image carries a second,
  separately configured build of one program.
- The base is a tag, so two builds of one commit can differ, and the release needs a Docker
  daemon that magus neither models nor caches.

What the image needs is small: take a base, add a few files a target built, set an
entrypoint and a few config fields, write the result. None of that executes anything. It is
a file transform over bytes, and a transform over bytes is something magus can run in the
sandbox, cache by its inputs and reproduce.

[ADR 0003](0003-running-an-invocation-on-another-operating-system.md) lists "build an image"
and "author a Dockerfile" under what magus never does. That line was written about running
an invocation inside a container, where building an image meant driving a runtime. Assembly
needs no runtime, so the line is narrowed by an amendment on that page rather than
contradicted here.

## Decision

### magus assembles an image layout from files a target built

The Go package `internal/image` reads an optional base from an OCI image layout on disk,
adds at most one new layer holding the files and symlinks a Spec lists, rewrites the
image config, and writes a new OCI image layout. A separate, explicit step publishes a
layout to a registry. `Index` joins single-platform layouts into one image index.

| Item                                                                                         | State    |
| -------------------------------------------------------------------------------------------- | -------- |
| `internal/image`: layer writer, config and manifest rewrite, layout read and write, `Index`  | proposed |
| The std `image` module, trampolines generated like `archive`                                 | proposed |
| Our own static image Spec, a Buzz file under `hack/magusfile/` that `magusfile.buzz` imports | proposed |
| Registry `Pull` and `Publish` over `internal/oci`, no go-containerregistry                   | proposed |
| The release path publishes the layout instead of running `docker buildx`                     | proposed |

<!--diagram:oci-assembly-->

### Go owns the rules, Buzz owns the spec

The rules, the bytes and the transfer live in Go. Buzz reaches them through the generated
bindings: a new file under `std/` declares a `std.Module` named `image`, and `magus-utils` generates
the trampolines the way it does for `archive`. A Spec is built by a Buzz function that returns what
to write. Go callers inside magus use `internal/image` with typed structs, through the
exported `std.Image*` functions. The module is not added to the public Go SDK; that needs
its own decision.

### This is not dockerfile support

Nothing here parses, accepts, translates or emulates a Dockerfile or a Containerfile. There
is no instruction mapping, no build context, no `RUN`, no `withExec`, no container
execution, no package installation and no runtime detection. A user with a Dockerfile keeps
using their container engine. This record reopens none of ADR 0003's refusals: magus still
runs no container, adds no container API to Buzz, and neither reads nor detects a runtime.

### Scope limits

- Platforms are `linux/amd64` and `linux/arm64` only. Windows images are out.
- One new Layer per `assembleLayout`. There is no control over layer splitting.
- No whiteouts and no deletion: a Spec cannot remove or replace a file a base layer holds.
- Sources are regular files. A directory, symlink, device, socket or FIFO source is an
  error.
- A base is named by digest only. Pulling or building from a mutable tag is refused.

### What this API cannot express

This list stays current with the module. Each item is an image magus does not produce, and
a user needing one keeps the container engine:

- An image that needs a command run at build time: compiling inside the image,
  `ldconfig`, `update-ca-certificates`.
- An image that installs distro packages, such as the dynamic variant's `inotify-tools`.
- An image that removes or replaces content of a base layer.
- A multi-stage image whose stages execute. Our stages are magus targets.

### Determinism, digest-pinned bases and loud failure

- Equal explicit inputs give byte-identical layouts: the same blobs, the same `index.json`
  and the same digests, independent of host path, file creation order, host mtimes, umask
  and `GOMAXPROCS`. The compiler's own output is outside this claim.
- Layer entries are sorted by destination. Gzip uses the standard library at a fixed
  level with no name and no mtime. Timestamps are fixed at Unix 0, as decided below.
- A base is a layout read from disk and verified: every blob is checked against its digest
  and size before use. Base layers, config and manifest bytes are reused unchanged. A
  base's platform that differs from the requested one is an error, and magus never
  relabels.
- Failure is loud and names the method: errors read `image.<method>: <what>: <detail>`.
  Invalid platforms, relative or unclean destinations, duplicate destinations, tag
  references and unsupported source types are errors that name the accepted set. There is
  no fallback and no default that substitutes for a missing input. A write that fails
  leaves the destination untouched.
- Every read and write goes through the sandbox checks, and every network call refuses
  when `MAGUS_OFFLINE` is set. Registry credentials are `secret.Value`, revealed only into
  the request header.

### The registry transport extends internal/oci

Decided by Eli on 2026-10-23. The registry transport extends `internal/oci` to carry image
manifests, indexes and blobs, and fixes its redirect guard and offline check. The OCI
descriptor, manifest, index and digest types are already direct dependencies.
`go-containerregistry` is not adopted: its `pkg/authn` imports `docker/cli/cli/config`, and
its `pkg/v1/remote` needs `authn`, so adopting it adds four modules: go-containerregistry,
docker/cli, docker-credential-helpers and logrus. A third, separate client is not an option.

### The timestamp policy is fixed at Unix 0

Decided by Eli on 2026-10-23. Every layer entry has mtime Unix 0. The gzip header has mtime
0 and no name. The config `created` field is copied from the base unless the Spec sets
one. History entries carry no `created`. A Spec that sets `created` to a commit time puts
that time into the target key through its input.

### Doctrine checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. A tag, an unsupported
  platform or a mismatched base fails with the accepted set named, and nothing is
  relabeled or substituted.
- [A record of refusals](../design/refusals.md): relied on. ADR 0003's
  refusal to run containers and expose a container API stands. The ledger rows for this
  record are added separately.
- The determinism rules follow the repository's reproducibility stance: output is a
  function of declared inputs.

## Alternatives

### A. Keep `docker` and `buildx` for the release image

Lost: it keeps a second build of the binary inside the image, keeps a mutable base tag, and
needs a daemon magus neither caches nor reproduces. Reopen if assembly cannot meet the
cannot-express list for the images the project must publish.

### B. Parse and execute `Dockerfile` files

Lost, and refused as a direction, not only for cost. It needs a parser, an instruction
mapping and container execution for `RUN`, which ADR 0003 refused to expose, and it makes
magus a build engine for another tool's language. Reopen only with new evidence about a
need assembly cannot meet, and with Eli's decision.

### C. Shell out to another image builder from a target

Lost for the release image: it adds a tool dependency and a non-reproducible step where a
deterministic transform is enough. A user may still write such an op in their own
magusfile, as ADR 0003 says for containers.

### D. Implement spec assembly in Go only, with no Buzz module

Lost: Specs belong in magusfiles, where a target's declared outputs feed them, so the
cache key covers the image's inputs. The module is the narrow seam that lets Buzz name
files and settings while Go owns the bytes.

## Consequences

- The release image stops depending on a Docker daemon and on a second compile of the
  binary, once the release path moves to the layout publish step. Until then the
  Dockerfiles stay.
- The `image` module adds a documented Buzz API and generated files under
  `internal/interp/bindings/gen/`, `std/gen/std-api.lock` and the reference docs, which are
  regenerated, not hand-edited.
- Images that need build-time commands or packages remain outside magus, and the list
  above is the place that says so.
- Our own static image gets the base pinned by digest, which the Dockerfile's
  `distroless/static:nonroot` tag does not give today.

## Open questions

1. **SBOM and provenance without buildx (D4).** Decided 2026-10-26 under Amendments: the
   `image` library owns `sbom`, `provenance` and `attach`/`attest`; cosign **sign** stays
   the existing spell. See that amendment.
2. **The dynamic image (D5).** The cgo variant with `inotify-tools` cannot be assembled
   without a package artifact or a runtime. Options: drop it, ship a statically linked
   watcher instead, or keep its Dockerfile until a design exists. Recommended: decide after
   the static cutover, with download numbers. Not decided.
3. **Assemble debugging and output verbs (D6).** Receipt/trace, size inventory, and Layout
   as the primary artifact are the stance (see the 2026-10-26 amendment). Open: whether a
   first-class `magus image inspect` / layout-to-tar / debugger-style step flags are needed
   beyond coded diagnostics and external OCI tools. Not decided.

## Amendments

### 2026-10-26: public libs nest, typed placements, auth secret, SBOM and provenance

Eli locked the packaging and the API contract after refining the Go and Buzz modules
together. The Decision above still describes the first cut (`internal/image`, string paths,
file-only sources, SBOM open). This amendment supersedes those rows where they conflict.
State stays _proposed_ until the code moves.

#### Packaging and dual modules

| Item                                                                                                                                              | State                                                           |
| ------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------- |
| `libs/oci`: distribution client moved from `internal/oci` (artifacts and images)                                                                  | done                                                            |
| `libs/image`: one package (types, layout I/O, assembleLayout, assembleIndex, pull/publish, sbom, provenance, attach)                              | done                                                            |
| Typed `Digest` / `DiffID` (sha256 only); digests on Layout are not bare strings                                                                   | done                                                            |
| Op, digests, counts, Created live on Layout / Published (facts only; no Notes log, no nested Report)                                              | done                                                            |
| Buzz module `image`: facade (`pullLayout`, `assembleLayout`, `assembleIndex`, `publishLayout`, `writeSbom`, `writeProvenance`, `attachArtifacts`) | done                                                            |
| Public Go API is `libs/image` and `libs/oci`; the root magus SDK does not re-export them                                                          | done                                                            |
| Nested `libs/image/{layout,assemble,index,…}` one-line reexports                                                                                  | not built (thin layers)                                         |
| `writeEach` multi-platform helper                                                                                                                 | not built (call-site loop + `assembleLayout` + `assembleIndex`) |
| Exported `Layer` + `NewLayer` (OCI fs-diff; Digest + DiffID)                                                                                      | done (still at most one new Layer per `assembleLayout`)         |
| Dockerfile vocabulary (`FROM`/`COPY`/`RUN`/…) in the Go or Buzz modules                                                                           | not built (see DevX below)                                      |
| Go `NewSpec` + pointer `With*` chain ending in `AssembleLayout`                                                                                   | done                                                            |
| Registry referrers `attest` after publish                                                                                                         | planned (`attachArtifacts` covers layout-local artifacts first) |

`libs/oci` stays a sibling of `libs/image`: spell and graph publish import the registry
client without assembly. Nesting registry under `image/` would force that dependency the
wrong way.

The module is named **`image`**, not a metaphor and not `container`: an Image is the OCI
artifact; a Container is a running instance. Public verbs are explicit (`assembleLayout` →
Image Layout with at most one new Layer; `assembleIndex` → Image Index; `pullLayout` /
`publishLayout` → registry; `writeSbom` / `writeProvenance` / `attachArtifacts` →
artifacts). Digests are `Digest` / `DiffID` (Buzz: `ImageDigest{algorithm,encoded}`), not
bare strings. Multi-arch is an explicit loop of `assembleLayout` then `assembleIndex`, not
a batch helper.

#### DevX: OCI packaging, not dockerfile theater

The packaging standard is OCI (Layout, Layer, Manifest, Index, Digest). That does **not**
mean the API speaks Dockerfile. Mapping `FROM`/`COPY`/`RUN` into a typed language recreates
Dagger's failure mode: callers inherit Docker's footguns and mental model, then paste CLI
strings into `RUN` because the abstraction never left the instruction set. magus already
refuses `RUN` and container execution; the API must not invite that vocabulary back.

| Keep (OCI / magus)                            | Refuse (Dockerfile / Docker-API cosplay)     |
| --------------------------------------------- | -------------------------------------------- |
| `Layer` / `NewLayer` (Digest + DiffID)        | Dockerfile "layer" as a build-cache metaphor |
| `WithBase` (layout path, digest-pinned)       | `From`, tag-based bases, build stages        |
| `WithFile` / `WithDir` / `Place`              | `Copy`, `Add`, build-context semantics       |
| config fields: Env, Entrypoint, Command, User | `Run`, `Shell`, exec-form theater            |
| `assembleLayout` / `assembleIndex`            | multi-stage Dockerfile, `buildx` flags       |

Go callers use ordinary idioms already in this repo: `NewSpec` / `NewMode` / `NewSecret` /
`NewLinuxAmd64` constructors, and `With*` methods that mutate a `*Spec` and return it for
chaining (the Dagger Go SDK pattern for options, not its Docker instruction map). Buzz still
passes a Spec map into `assembleLayout`; the chain is the Go call-site shape.

#### Typed placements, modes, directories

Host and image paths are typed (`HostFile`, `HostDir`, `ImageFile`, `ImageDir`) with base
composition, not bare strings. A `Placement` carries source, destination, `Mode`, uid and
gid. `Mode` is an `enum<int>` (`file`, `executable`, `private`, `readonly`); raw permission
ints go through a validating constructor.

Directory sources are in scope as `DirPlacement`: `strip` (tar `--strip-components` on
`basename(source)/rel`: 0 keeps the source directory name, 1 places contents, 2+ peels
inner prefixes), `include` / `exclude`
globs, `maxDepth`, `modePolicy` (`set` | `preserve`), `fileMode` / `dirMode`, uid/gid
(always set on the placement; host owners are not portable), `symlinks` (`error` | `keep` |
`follow`, default `error`), `emptyDirs`, and size/count caps. Devices, sockets, FIFOs and
hardlinks as sources remain errors. mtimes stay Unix 0. Overlap between placements is an
error. This replaces the Decision's "sources are regular files only" limit for directories
that opt into `DirPlacement`; undeclared directory sources stay refused.

#### Multi-architecture (not dockerfile multi-stage)

`assembleLayout` produces one platform's **Image Manifest** (and at most one new **Layer**).
Multi-arch is a call-site loop of `assembleLayout` (one dest per platform) then
`assembleIndex` to join those layouts into an **Image Index**. Dockerfile multi-stage
execution is still refused: magus targets produce files; `assembleLayout` consumes them.
No `FROM`/`RUN`/`COPY` trailing-slash mapping.

#### Registry auth uses `Secret`

`pull` and `publish` take optional `RegistryAuth{ username, password: Secret }`. `Secret`
matches the provider boundary shape (`value`); Go wraps it as `secret.Value` at the call.
A bare string password is rejected. No docker-config auto-load (no silent default).
Anonymous pull remains valid for public registries.

#### SBOM and provenance (closes D4)

| Method                                  | Role                                                                                  |
| --------------------------------------- | ------------------------------------------------------------------------------------- |
| `sbom(layout, …)`                       | SPDX document from the layout filesystem                                              |
| `provenance(layout, ProvenanceSpec, …)` | in-toto / SLSA statement from explicit materials (base digests, revision, builder id) |
| `attach(layout, sbom?, provenance?)`    | bind attestations onto the Image Index before publish                                 |
| `attest(published, …)`                  | optional post-publish referrers path                                                  |

Cosign **sign** stays the existing cosign spell. Provenance materials are explicit: nothing
guesses the GitHub run. Prefer `attach` before `publish` so one push carries image and
attestations.

#### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on for digest-only
  refs, typed auth, explicit `DirPlacement.strip` (no trailing-slash COPY), and provenance
  materials named by the caller.
- [One vocabulary](../glossary/one-vocabulary-rule.md): Image / Image Index / Image Layout;
  never Container for the artifact; package id `image` matches the OCI noun.
- [A copy can be the right answer](../design/principles/a-copy-can-be-the-right-answer.md):
  `libs/oci` and `libs/image` stay separate rather than one nested tree, because their
  importers and failure domains differ.

### 2026-10-26: goals, non-goals, seams, mounts, debugging, examples

#### Goals and non-goals

**Goals:** teach OCI (Layout, Layer, Manifest, Index, Digest); typed Buzz/Go Specs;
tiny images as the easy path; determinism; loud failure; digests agents can assert.

**Non-goals:** Dockerfile parse/execute; in-image `RUN` / package install; BuildKit
`RUN --mount=type=cache|ssh` verbs on `image.*`; silent `$HOME`/`$VAR` expansion inside
Spec source paths; Spec scaffolding generators; Windows container images (host OS ≠ image OS);
auto-pull of a missing base (refresh is `pullLayout` under the `update` charm only).

#### Seams

| Seam                       | Owns                                                          | Does not own                  |
| -------------------------- | ------------------------------------------------------------- | ----------------------------- |
| magus **target**           | Compile, fetch, Secret, SSH agent, tool caches                | OCI layers and config         |
| **`image` / `libs/image`** | Placements, argv (`command`/`entrypoint`), layout I/O, attach | Shells, apt, BuildKit mounts  |
| **`libs/oci`**             | Registry transport                                            | Specs / assemble              |
| **sandbox**                | Host path confinement (`filesystem.Under`)                    | Image-destination POSIX rules |

Home/env resolution stays at the call site (`env\home`, `path\expandUser`, `env\require`),
then a concrete host path enters the Spec. Private git and caches are **target** patterns;
document them; do not add `image\withSSHMount`.

Guide: [Assemble an OCI image with magus](../guides/setup/assemble-oci-image.md).
Examples: [`std/examples/image/`](https://github.com/egladman/magus/tree/main/std/examples/image/), mapped from Docker
Official Image Dockerfiles (hello-world scratch; busybox rootfs tarball; golang multi-stage
_shape_; multi-arch Index; digest-pinned base + config overlay; nginx/redis/python
package-install non-goals).

#### Debugging and outputs (open: D6)

Direction: not all shipped as CLI flags yet:

- Receipt facts on every Layout (op, digests, counts, layer size, **Created**).
  Age is derived by the caller from `Created`, not baked into the receipt as prose.
- Size inventory / IMG diagnostics for fat `DirPlacement` and empty `User`.
- Primary artifact output is an **OCI Layout directory** (plus optional publish digest);
  SBOM/provenance are files from `writeSbom` / `writeProvenance` then `attachArtifacts`.
- Prefer coded diagnostics over a global debug soup; layout dirs are already inspectable
  with standard OCI tools (`skopeo`, `oras`, `dive` on exported forms).
- Registry I/O is offline-first: `assembleLayout` / `assembleIndex` never network;
  `pullLayout` / `publishLayout` are explicit. A target that refreshes a cached base
  gates the pull on `:update` (no magus `--pull` flag). Empty `Created` means the
  config omitted it; speak that at the call site, not as Layout.Notes.

Candidate verbs/flags (undecided): receipt dump (`-o receipt` / JSON Lines), inventory,
compare two layouts by digest, optional layout→oci-archive only if Layout dirs prove
insufficient. Analogy to a debugger: inspect the produced Layout and receipt, not a parallel
"debug assemble" dialect.

Reopens if someone needs a first-class `magus image inspect` / layout-to-tar verb with
evidence the Layout directory is not enough.

### 2026-10-27: targets, OCI vocabulary, one schema

**Paradigm.** No program runs inside an image, and the module has no `run`. Each stage,
layer, platform, index and push is a magus target: a stage runs its tool on the host, a
layer is one `image\assemble` over the previous target's layout, a platform is one target,
an Image Index is a target that `ctx.needs` every platform target, and a push is a
`skip_cache` target. Fibers add no parallelism (a host call blocks the fiber's thread);
targets and `ctx.needs` do. Multi-stage has two shapes and no core change to cache keys or
needs ordering (MGS4010 stays as it is): stages in other projects, whose declared outputs a
cross-project `needs` finishes before the consumer starts; or stages in one project, where
the image target lists the stage's sources in `readsFiles`, never its output
(`TestStageInOneProjectCachesAndRekeysOnItsSource` pins the cache hit and the rekey).

**Verbs and names are OCI's.** `Cmd`, not command. The module namespaces its records, so
their Buzz names drop the `Image` prefix (`image\Spec`, `image\Platform`). No doc defines a
thing by what Docker calls it.

**Config follows image-spec config.md.** A null field inherits the base's value and an
empty list clears it; `ExposedPorts`, `Volumes` and `StopSignal` are added; the reset
booleans are gone.

**Placements.** `Tree` replaces `DirPlacement`: it places the contents of its source;
`strip`, the size and depth caps and the umask-dependent "preserve" mode are deleted.
`Directory` places a directory with its own mode and owner. Implicit parents are written
only on scratch: over a base an upper directory entry would replace the base's mode and
owner.

**One schema.** The Buzz boundary decodes every image argument with the generated strict
decoder; an unknown or wrong-typed member raises. Closed sets are registered enums, each
starting with an unset case that Go refuses, so an omitted platform is never linux/amd64.
`fs\mode` builds a mode from octal or rwx notation; mode 0 is refused. Registry auth takes
the `Secret` mirror, never a string.

**Digests.** `ImageDigest{algorithm, encoded}` stays at the Buzz boundary and has
`string()` (Go: `String()`) returning `sha256:<hex>`.

**Referrers.** `attachReferrers` writes OCI 1.1 artifact manifests with `subject` set to
the image's root descriptor and the empty config descriptor, to a new layout. The subject
layout is never edited. Push sends the referrers after the image.

**Pull.** A digest-pinned pull is an ordinary cacheable op. A tag-only pull is refused
outside the `update` charm, which resolves the tag and then pulls the digest. Pull fetches
straight into the new layout, hashing each blob once. Platforms compare normalized:
linux/arm64 matches linux/arm64/v8.

**Determinism caveat.** Layer bytes depend on compress/flate, which Go does not pin across
releases: a toolchain bump may change layer digests, and the determinism claim excludes it.
Base blobs are size-checked, not rehashed, on reuse. A base blob is hardlinked only when it
is already 0644, so assemble never changes another layout's file.

#### OCI spec conformance

The image-spec v1.1.1 and distribution-spec v1.1.1 MUSTs the first cut broke are fixed,
each pinned by a test in `libs/oci` or `libs/image`. This supersedes the 10-26 rows that
refused Docker media types and that read index.json by position.

| Rule                                                                       | State   | Pinned by                                                                |
| -------------------------------------------------------------------------- | ------- | ------------------------------------------------------------------------ |
| `docker.io` and `index.docker.io` go to `registry-1.docker.io`; a one-segment Docker Hub name is refused naming `library/` | built | `TestDockerHubRequestsGoToRegistry1AndABareNameIsRefused` |
| Pull and Push copy Docker schema 2 and unknown layer types as served; a non-distributable layer is described, not fetched or uploaded | built | `TestPullCopiesADockerImageAndAssembleConvertsIt`, `TestPullDescribesANonDistributableLayerAndAssembleRefusesIt` |
| Assemble converts a Docker base to OCI and refuses a non-distributable or unknown base layer | built | same two tests |
| A referrer pushed to a registry that answers without `OCI-Subject` is added to the `sha256-<hex>` tag index; `Client.Referrers` reads the Referrers API, then that tag on a 404 | built | `TestPushManifestKeepsTheReferrersTagWhenTheRegistryHasNoReferrersAPI`, `TestPushThenPullRoundTripsAnAttachedIndex` |
| A layout's image is the index.json entry with no subject, chosen by `org.opencontainers.image.ref.name` when there are several; referrers are the entries whose subject it is; unknown entries are passed over | built | `TestOpenLayoutSelectsAnImageByRefNameAndPassesOverUnknownEntries`, `TestOpenLayoutFindsReferrersBySubject` |
| Pull records the reference pinned at the platform manifest, so `base.name` and `base.digest` name the same manifest | built | `TestBaseNameAndBaseDigestNameTheSameManifest` |
| Config members the image-spec types do not model are carried over from the base | built | `TestAssembleKeepsTheBaseConfigFieldsItDoesNotModel` |
| A placed path named `.wh.*` is refused | built | `TestAssembleRefusesAWhiteoutName` |
| A registry error body is parsed into its codes; 403 is `ErrDenied`; a 429 with a short `Retry-After` is retried; `Warning: 299` is logged | built | `TestStatusErrorReadsTheSpecErrorBody`, `TestTooManyRequests*`, `TestRegistryWarningIsLogged` |
| Platform carries `os.version` and `os.features`; amd64 takes variants v1 to v3 | built in Go | `TestPlatformCarriesOSVersionAndOSFeatures`, `TestParsePlatformRoundTrips` |
| index.json carries its `mediaType` | built | `TestIndexJSONNamesItsMediaType` |
| Buzz `mediaType` and `artifactType` are `str`, with the media types as named constants | not built | the boundary decoders and declarations are generated outputs another lease holds |
| Buzz `osVersion`, `osFeatures`, amd64 `v2`/`v3`, `image\open(layout, name)` | not built | same |

Go names follow the verb rule: `Get` for a network read (`oci.Client.GetManifest`,
`GetBlob`), `Push` for an upload (`PushManifest`, `PushBlob`), `Write` only for a file
(`WriteSBOM`, `WriteProvenance`). `image.Pull(ctx, client, ref, platform, dest)` and
`image.Push(ctx, client, layout, repo, tags...)` take every required input as a parameter
and Push returns the pinned `oci.Reference`; `PullOptions`, `PushOptions`, `AllowTag` and
`Pushed` are deleted. `ManifestLayout` is `Manifest`, `Layout.Manifest(ctx, platform)`
replaces `ResolvePlatform`, the layer receipt `Added` is `Layer`, the policy constants carry
their type's name (`ModePolicyFixed`, `SymlinkPolicyKeep`), `Tree.Modes` is
`Tree.ModePolicy`, `Provenance.Materials` is SLSA v1's `Dependencies`, and `Verify` and
`ParseDigest` moved to `oci.VerifyBlob` and `oci.ParseDigest`. Running a finished image is
a later decision and changes nothing here.

#### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): a bare Docker Hub name
  is refused rather than completed with `library/`; a base config's unmodelled members are
  kept rather than dropped; a whiteout name is refused rather than placed.
- [One vocabulary](../glossary/one-vocabulary-rule.md): blob, referrer, subject,
  `ref.name` and `Get`/`Push` are the specs' words.
