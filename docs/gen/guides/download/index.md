---
title: Download
page_type: overview
description: Download a signed release of magus, verify it with the embedded Ed25519 key, and keep it current with magus self update.
tags: [download, install, release, self-update, ed25519, verify, signing]
---

# Download

magus ships as a single self-contained binary. Pick your platform for step-by-step install and PATH setup, then verify the signature before you run it.

## Install

Each platform guide has a copy-paste `curl` install that pulls the current release, extracts the binary onto your `PATH`, and points you at verification:

- [Linux](download/linux.md) - amd64 or arm64
- [macOS](download/macos.md) - Apple Silicon or Intel
- [Windows](download/windows.md) - amd64
- [Docker](download/docker.md) - run in a container instead of installing a binary (any platform)

Prefer a prebuilt binary or a local build? See [Install prebuilt binary](#install-prebuilt-binary) and [Build from source](#build-from-source). Already using a version manager? See [mise](#mise).

Every build is published to [GitHub Releases](https://github.com/egladman/magus/releases) alongside its `SHA256SUMS` manifest and signature. However you install, [verify the release](#verify-a-release) first.

## Update

`magus self update` fetches the latest release, verifies the signature against the key baked into your binary, and swaps in place. Full flag reference: [`magus self`](../reference/manpage/magus-self/).

| Flag               | Effect                                 |
| ------------------ | -------------------------------------- |
| `--check`          | Report availability without installing |
| `--dry-run`        | Fetch and verify but do not swap       |
| `--version v0.4.2` | Pin to a specific tag                  |
| `--force`          | Allow downgrade or reinstall           |
| `--bin-dir <path>` | Install elsewhere instead of in place  |
| `-y` / `--yes`     | Skip the confirmation prompt           |

Package-maintainer builds compiled with `-tags noselfupdate` disable this subcommand; fall back to a manual [install](#install).

## Verify a release

Alongside the binary tarballs, each release ships `SHA256SUMS` (the manifest) and `SHA256SUMS.sig` (its Ed25519 signature). All artifacts, plus the signing key, are attached to each [GitHub release](https://github.com/egladman/magus/releases); [/public/](../../public/) indexes the releases and the machine-readable manifest.

**Already running magus?** Use the built-in verifier:

```sh
magus self update --dry-run
```

The trust chain runs through your already-trusted binary. Nothing else to do.

**First install - verify by hand.** Do _not_ verify a fresh magus with itself: a tampered build carries the attacker's key and self-reports success. Use OpenSSL with the key served from this HTTPS page.

1. Save the key. Either [download magus-release.pem](../assets/magus-release.pem), or copy the PEM block below into `magus-release.pem`.

2. Verify the manifest signature (requires OpenSSL 3.0+):

   ```sh
   openssl pkeyutl -verify -pubin -inkey magus-release.pem \
     -rawin -in SHA256SUMS -sigfile SHA256SUMS.sig
   # Signature Verified Successfully
   ```

3. Only if the signature verifies, check the artifact hash:

   ```sh
   sha256sum -c SHA256SUMS 2>/dev/null | grep magus_
   # macOS: shasum -a 256 -c SHA256SUMS
   ```

Order matters. Checking a hash against an unverified manifest proves nothing.

### Release signing key (Ed25519)

```text
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEA/7uPpvNidN79EoiAk8ajIsJTK8VFAW9JWrSVXey2Z3k=
-----END PUBLIC KEY-----
```

Raw base64 (32 bytes):

```text
/7uPpvNidN79EoiAk8ajIsJTK8VFAW9JWrSVXey2Z3k=
```

The key is embedded in every magus binary via `//go:embed`, so `magus self update` trusts it transitively. A planned rotation first ships a release signed by the current key that embeds the replacement key; later releases can use the replacement. Older binaries cannot be remotely revoked if the current key is compromised. The maintainer procedure is in the [contributing guide](../development/contributing/).

## Shell completion

```sh
magus completion <shell>    # bash / zsh / fish / powershell (or pwsh)
```

See [`magus completion`](../reference/manpage/magus-completion/) for the exact source-and-persist recipe per shell.

## `mgs` shorthand

The de facto shorthand for `magus` is `mgs`: three left-hand keys, fast to type, and collision-free. Alias it in your shell:

```sh
alias mgs=magus
```

Or create a symlink:

```sh
ln -s "$(command -v magus)" ~/.local/bin/mgs
```

## Install prebuilt binary

### A. Recommended


``` sh
curl --proto '=https' --tlsv1.2 -sSf https://eli.gladman.cc/magus/install -o install.sh
less install.sh
sh install.sh
```

Review the script first: it lets you inspect the download, signature verification, and
installation steps before granting a network response access to your shell.

The installer selects the static binary by default. Pass `--variant dynamic` only
when you specifically need the platform-native build and that release publishes it.


### B. Yolo

``` sh
curl --proto '=https' --tlsv1.2 -sSf https://eli.gladman.cc/magus/install | sh
```

## mise

magus installs through [mise](https://mise.jdx.dev) with no plugin, via its
[ubi](https://mise.jdx.dev/dev-tools/backends/ubi.html) backend, which pulls the
GitHub release asset matching your platform:

```bash
mise use -g ubi:egladman/magus@v0.3.0
```

Pin it per repository by putting it in that repo's `mise.toml` instead:

```toml
[tools]
"ubi:egladman/magus" = "v0.3.0"
```

Three things to know before choosing this route.

**mise owns the version, not `magus self update`.** Upgrade with `mise upgrade`.
Running `magus self update` on a mise-managed install would replace a binary mise
believes it controls, and the next `mise install` would undo it.

**Nothing verifies the release signature.** ubi fetches the asset over HTTPS and
trusts GitHub; it performs no signature or checksum check of its own. The platform
installers and `magus self update` both verify the artifact against the
Ed25519-signed manifest. If that guarantee matters,
[verify the release](#verify-a-release) by hand afterwards, or use the platform
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

## Changelog

See the [CHANGELOG](https://github.com/egladman/magus/blob/main/CHANGELOG.md).
