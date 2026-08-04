---
title: cosign spell
description: "Cosign spell: sign, attest, verify, verify-attestation, and the blob pair for artifacts and images."
tags: [cosign, spell, sigstore, signing, supply-chain, tools]
---

# cosign

The `cosign` spell forks the Sigstore `cosign` CLI to sign, attest, and verify artifacts - registry images and plain files (the blob pair) alike. Signing and attestation pass `--yes` for non-interactive (CI) use. Credentials ride the environment, and the sandbox scrubs it: pass `COSIGN_*` (and, for keyless OIDC in GitHub Actions, `ACTIONS_ID_TOKEN_REQUEST_URL`/`ACTIONS_ID_TOKEN_REQUEST_TOKEN`) through `sandbox.env.passthrough` in magus.yaml, or signing fails with an auth error that never names the scrub.

**Runtime name:** `cosign` (source `spells/cosign/`)

**Version probe:** `cosign version`

## Passing arguments to ops

Every op is invoked as `cosign["<op>"](ctx, opts?)`. The first argument is the target's context, which is what carries the execution environment; the optional options map shapes the command itself:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `cosign["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L187) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L191) |


Working directory and environment are NOT options: they ride the context, as `cosign["<op>"](ctx.withCwd("sub"))` and `cosign["<op>"](ctx.withEnv({"CGO_ENABLED": "0"}))`. Only the context reaches the cache key, so an option-table cwd or env would change what the tool did while the key said otherwise - passing either as an option is an error.

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## cosign-attest

**Command:** `cosign attest --yes`

### Example

<!-- magus-run-recorder -->
```buzz
// cosign-attest attaches a signed attestation; pass the predicate file, its type,
// and the image reference.
import "magus";
import "magus/spell/cosign";

magus\project({ "spells": [cosign] });

export fun attest(ctx: magus\Context, args: [str]) > void {
    cosign["cosign-attest"](ctx, { "args": ["--predicate", "sbom.json", "--type", "cyclonedx", "app:latest"] });
}
```

## cosign-sign

--yes skips the interactive transparency-log confirmation so signing/attesting runs unattended; the caller appends the target reference and flags.

**Command:** `cosign sign --yes`

### Example

<!-- magus-run-recorder -->
```buzz
// cosign-sign signs an artifact keyless (--yes for CI); pass the image reference
// to sign, so `magus run sign` forks `cosign sign --yes app:latest`.
import "magus";
import "magus/spell/cosign";

magus\project({ "spells": [cosign] });

export fun sign(ctx: magus\Context, args: [str]) > void {
    cosign["cosign-sign"](ctx, { "args": ["app:latest"] });
}
```

## cosign-sign-blob

The blob pair signs and verifies plain files - exactly what a build tool produces (archives, binaries, manifests) - not just registry artifacts.

**Command:** `cosign sign-blob --yes`

## cosign-verify

**Command:** `cosign verify`

### Example

<!-- magus-run-recorder -->
```buzz
// cosign-verify checks an image's signature; pass the image reference (add
// --certificate-identity / --certificate-oidc-issuer for keyless verification).
import "magus";
import "magus/spell/cosign";

magus\project({ "spells": [cosign] });

export fun verify(ctx: magus\Context, args: [str]) > void {
    cosign["cosign-verify"](ctx, { "args": ["app:latest"] });
}
```

## cosign-verify-attestation

verify-attestation is attest's gate: without it a pipeline can produce attestations but never require them, which halves the supply-chain story. The caller appends --type and the identity/key flags along with the ref.

**Command:** `cosign verify-attestation`

## cosign-verify-blob

**Command:** `cosign verify-blob`

