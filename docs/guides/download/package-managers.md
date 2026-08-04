---
title: Package managers and source builds
order: 5
description: Install magus through mise via the ubi backend, or build it from source, and the tradeoffs each route makes against the signed-release guarantee.
tags: [mise, ubi, aqua, package-manager, build-from-source, noselfupdate]
---

# Package managers and source builds

Both routes on this page trade something away against the [install script](../download.md#install). Read the caveats before choosing one.

## mise

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

### Why not aqua, or the go backend?

[aqua](https://mise.jdx.dev/dev-tools/backends/aqua.html) is mise's preferred
backend where a tool is registered, and it is the better target long-term because
the aqua registry carries checksum and signature metadata that ubi has no
equivalent for. It is not usable yet: magus has no aqua-registry entry, so
`aqua:egladman/magus` fails with `no aqua-registry found`. Getting one is an
upstream pull request to
[aquaproj/aqua-registry](https://github.com/aquaproj/aqua-registry), and it would
close the verification gap above.

The `go` backend resolves and compiles, but do not use it for an install you
intend to keep:

```bash
# builds, but reports: magus unknown (unknown) built unknown
mise use -g go:github.com/egladman/magus/cmd/magus@latest
```

`go install` cannot pass the `-ldflags` that stamp the version, commit, and build
date. `unknown` is not cosmetic - it is the dev-build sentinel magus keys on
internally to fingerprint an unstamped build, so a go-backend install presents
itself to magus as a development binary rather than the release it came from.
Use it to try magus, not to run it.

## Build from source

```sh
git clone https://github.com/egladman/magus
cd magus
go build -o magus ./cmd/magus
```

Add `-tags noselfupdate` to disable the self-update subcommand (for distro-packaged builds).
