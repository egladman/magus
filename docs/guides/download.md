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

Prefer Go tooling or a local build? See [Install with Go](#install-with-go) and [Build from source](#build-from-source).

Every build is published at [/public/release/](../../public/release/) alongside its `SHA256SUMS` manifest and signature. However you install, [verify the release](#verify-a-release) first.

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

Alongside the binary tarballs, each release ships `SHA256SUMS` (the manifest) and `SHA256SUMS.sig` (its Ed25519 signature). All artifacts, plus the signing key, are listed at [/public/release/](../../public/release/).

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

The key is embedded in every magus binary via `//go:embed`, so `magus self update` trusts it transitively. Rotating the key requires shipping a new release built with the new embedded key; older binaries continue to trust only the previous key.

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

## Install with Go

`go install github.com/egladman/magus/cmd/magus@latest` is **not currently supported.** magus vendors its embedded Buzz interpreter through a local `replace` directive in `go.mod`, and `go install` refuses any module whose `go.mod` carries a `replace` - it fetches the tagged source from the module proxy, where a local path cannot resolve, and fails with:

```text
The go.mod file for the module providing named packages contains one or
more replace directives. It must not contain directives that would cause
it to be interpreted differently than if it were the main module.
```

Until the interpreter is published as a standalone, versioned module, install a [prebuilt binary](#install) (recommended) or build from a clone below - a clone works because the `replace` resolves against the checked-out `./libs/gopherbuzz`.

## Build from source

```sh
git clone https://github.com/egladman/magus
cd magus
go build -o magus ./cmd/magus
```

Add `-tags noselfupdate` to disable the self-update subcommand (for distro-packaged builds).

## Changelog

See the [CHANGELOG](https://github.com/egladman/magus/blob/main/CHANGELOG.md).
