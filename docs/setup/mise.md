---
title: mise
description: Install magus through mise via the ubi backend, and why the aqua and go backends are not the route to take.
tags: [mise, ubi, aqua, package-manager, go-install]
---

# mise

magus installs through [mise](https://mise.jdx.dev) with no plugin, via its
[ubi](https://mise.jdx.dev/dev-tools/backends/ubi.html) backend, which pulls the
GitHub release asset matching your platform:

```bash
mise use -g ubi:egladman/magus@__MAGUS_VERSION__
```

Pin it per repository by putting it in that repo's `mise.toml` instead:

```toml
[tools]
"ubi:egladman/magus" = "__MAGUS_VERSION__"
```

Three things to know before choosing this route.

**mise owns the version, not `magus self update`.** Upgrade with `mise upgrade`.
Running `magus self update` on a mise-managed install would replace a binary mise
believes it controls, and the next `mise install` would undo it.

**Nothing verifies the release signature.** ubi fetches the asset over HTTPS and
trusts GitHub; it performs no signature or checksum check of its own. The platform
installers and `magus self update` both verify the artifact against the
Ed25519-signed manifest. If that guarantee matters,
[verify the release](verify.md) by hand afterwards, or use the platform
installer instead.

**You may get a different binary than the installer hands you.** The installer
defaults to the static build; ubi resolves to the dynamically linked one where a
release publishes both. `darwin/amd64` currently ships only a static build, so
that is what it gets there.

## Why not aqua, or the go backend?

[aqua](https://mise.jdx.dev/dev-tools/backends/aqua.html) is mise's preferred
backend where a tool is registered, and it is the better target long-term because
the aqua registry carries checksum and signature metadata that ubi has no
equivalent for. It is not usable yet: magus has no aqua-registry entry, so
`aqua:egladman/magus` fails with `no aqua-registry found`. Getting one is an
upstream pull request to
[aquaproj/aqua-registry](https://github.com/aquaproj/aqua-registry), and it would
close the verification gap above.

The `go` backend resolves and compiles. It is documented here because people find
it anyway, not because it is supported.

> **Not the golden path, and not a route to adopt.** magus is a build tool, and a
> build tool installed through the package manager of a toolchain it manages puts
> a circle in your foundation: you need a working Go toolchain to obtain the thing
> you use to manage Go builds, and when something breaks the remedy is to upgrade
> the toolchain you were using magus to pin. Those failures are oblique, hard to
> guard against, and there is usually nowhere sensible to attach an error message
> explaining what happened. The same objection applies to installing any build
> tool with npm, cargo, or pip. Use the [install script](../setup.md#install).

```bash
# builds, but reports: magus unknown (unknown) built unknown
mise use -g go:github.com/egladman/magus/cmd/magus@latest
```

Two concrete problems on top of the structural one.

`go install` cannot pass the `-ldflags` that stamp the version, commit, and build
date. `unknown` is not cosmetic - it is the dev-build sentinel magus keys on
internally to fingerprint an unstamped build, so a go-backend install presents
itself to magus as a development binary rather than the release it came from.

It used to fail outright on many clean machines, and the fix is worth knowing about
if you are packaging magus. `internal/codec` selected its implementation on the
`cgo` build tag, and `CGO_ENABLED` defaults to 1 wherever a C compiler is present,
which covers a typical Linux dev box and any Mac with the Xcode command line tools.
That path needs `liblzma` and `libzstd` development headers discoverable by
pkg-config, so without them the build died at the pkg-config step with an error
naming neither magus nor the fix. It was invisible from a maintainer's machine,
where the headers are always present.

The native codec is now opt-in, one tag per system library, so every build that does
not ask for it gets the pure-Go implementation that static releases have always
shipped. Only the dynamically linked release asset asks:

```sh
go build -tags liblzma,libzstd ./cmd/magus
```

