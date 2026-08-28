---
title: Verify a release
description: Verify a magus release against its Ed25519-signed SHA256SUMS manifest, by hand on first install or with the built-in verifier afterwards.
tags: [verify, signature, ed25519, sha256, openssl, release, security]
---

# Verify a release

Alongside the binary tarballs, each release ships `SHA256SUMS` (the manifest) and `SHA256SUMS.sig` (its Ed25519 signature). All artifacts, plus the signing key, are attached to each [GitHub release](https://github.com/egladman/magus/releases); [/public/](../../../public/) indexes the releases and the machine-readable manifest.

**Already running magus?** Use the built-in verifier:

```sh
magus self update --dry-run
```

The trust chain runs through your already-trusted binary. Nothing else to do.

**First install - verify by hand.** Do _not_ verify a fresh magus with itself: a tampered build carries the attacker's key and self-reports success. Use OpenSSL with the key served from this HTTPS page.

1. Save the key. Either [download magus-release.pem](../../assets/magus-release.pem), or copy the PEM block below into `magus-release.pem`.

2. Verify the manifest signature (requires OpenSSL 3.0+):

   ```sh
   openssl pkeyutl -verify -pubin -inkey magus-release.pem \
     -rawin -in SHA256SUMS -sigfile SHA256SUMS.sig
   # Signature Verified Successfully
   ```

3. Only if the signature verifies, check the artifact hash:

   ```sh
   sha256sum --ignore-missing -c SHA256SUMS
   # macOS: shasum -a 256 --ignore-missing -c SHA256SUMS
   ```

   `--ignore-missing` skips manifest entries for artifacts you did not download,
   so the output stays limited to the file you fetched - without piping through
   `grep`, which would replace the command's exit status with `grep`'s and let a
   failed check report success.

Order matters. Checking a hash against an unverified manifest proves nothing.

## Release signing key (Ed25519)

```text
-----BEGIN PUBLIC KEY-----
MCowBQYDK2VwAyEA/7uPpvNidN79EoiAk8ajIsJTK8VFAW9JWrSVXey2Z3k=
-----END PUBLIC KEY-----
```

Raw base64 (32 bytes):

```text
/7uPpvNidN79EoiAk8ajIsJTK8VFAW9JWrSVXey2Z3k=
```

The key is embedded in every magus binary via `//go:embed`, so `magus self update` trusts it transitively. A planned rotation first ships a release signed by the current key that embeds the replacement key; later releases can use the replacement. Older binaries cannot be remotely revoked if the current key is compromised. The maintainer procedure is in the [contributing guide](../../development/contributing/).
