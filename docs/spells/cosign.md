---
title: cosign spell
description: "Cosign spell: keyless sign, attest, and verify for container artifacts."
tags: [cosign, spell, sigstore, signing, supply-chain, tools]
---

# cosign

The `cosign` spell forks the Sigstore `cosign` CLI to sign, attest, and verify artifacts. Signing and attestation pass `--yes` for non-interactive (CI) use.

**Runtime name:** `cosign` (source `spells/cosign/`)

**Version probe:** `cosign version`

## Passing arguments to ops

Every op is invoked as `cosign["<op>"](opts?)`, where the optional options map accepts these keys - all optional, each appended to or shaping the forked command:

| Key | Type | Description | Source |
|-----|------|-------------|--------|
| `args` | `[str]` | Extra arguments appended to the resolved command. Omit it and a bare `cosign["<op>"]()` forwards `magus run <target> -- <extra>` to the tool automatically; pass it to set the arguments explicitly, which replaces that passthrough. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L108) |
| `cwd` | `str` | Working directory the command runs in. Defaults to the project directory. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L105) |
| `env` | `{str: str}` | Environment variables set for the process, on top of the inherited environment. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L112) |
| `stdin` | `str` | Data written to the command's standard input. | [source](https://github.com/egladman/magus/blob/main/internal/interp/bindings/spell_object.go#L120) |

Charms (the `:charm` suffix, e.g. `magus run test:rw`) are orthogonal: they patch the base argv, while these options add to it. See [Charms](../charms.md).

## cosign-attest

**Command:** `cosign attest --yes`

### Example

<!-- run-recorder -->
```buzz
// cosign-attest attaches a signed attestation; pass the predicate file, its type,
// and the image reference.
import "magus";
import "magus/spell/cosign";

magus.project({ "spells": [cosign] });

export fun attest(args: [str]) > void {
    cosign["cosign-attest"]({ "args": ["--predicate", "sbom.json", "--type", "cyclonedx", "app:latest"] });
}
```

## cosign-sign

--yes skips the interactive transparency-log confirmation so signing/attesting runs unattended; the caller appends the target reference and flags.

**Command:** `cosign sign --yes`

### Example

<!-- run-recorder -->
```buzz
// cosign-sign signs an artifact keyless (--yes for CI); pass the image reference
// to sign, so `magus run sign` forks `cosign sign --yes app:latest`.
import "magus";
import "magus/spell/cosign";

magus.project({ "spells": [cosign] });

export fun sign(args: [str]) > void {
    cosign["cosign-sign"]({ "args": ["app:latest"] });
}
```

## cosign-verify

**Command:** `cosign verify`

### Example

<!-- run-recorder -->
```buzz
// cosign-verify checks an image's signature; pass the image reference (add
// --certificate-identity / --certificate-oidc-issuer for keyless verification).
import "magus";
import "magus/spell/cosign";

magus.project({ "spells": [cosign] });

export fun verify(args: [str]) > void {
    cosign["cosign-verify"]({ "args": ["app:latest"] });
}
```

