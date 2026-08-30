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
# The hosted docs substitute the latest release tag below; on GitHub it reads
# literally - get the real value from https://github.com/egladman/magus/releases
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

The `go` backend does not currently work at all. It is documented here because
people find it anyway, not because it is supported.

> **Not the golden path, and not a route to adopt.** magus is a build tool, and a
> build tool installed through the package manager of a toolchain it manages puts
> a circle in your foundation: you need a working Go toolchain to obtain the thing
> you use to manage Go builds, and when something breaks the remedy is to upgrade
> the toolchain you were using magus to pin. Those failures are oblique, hard to
> guard against, and there is usually nowhere sensible to attach an error message
> explaining what happened. The same objection applies to installing any build
> tool with npm, cargo, or pip. Use the [install script](../setup.md#install).

```bash
# fails outright, before it ever reaches a compile step
mise use -g go:github.com/egladman/magus/cmd/magus@latest
```

`go.mod` requires `github.com/egladman/magus/libs/diagnostics` and
`.../libs/gopherbuzz` - nested modules with their own `go.mod` - at `v0.0.0`,
resolved only through this repo's own LOCAL `replace` directives; neither nested
module has ever had a tagged release. `go install pkg@version` (what the `go`
backend runs under the hood) refuses outright to build any module whose `go.mod`
contains a `replace` directive, local or not, unless that module is the main
module of the build - so the install dies on the replace directives themselves,
before dependency resolution or compilation ever starts. There is no flag that
gets around this from the consuming side; it is fixed only by the nested modules
getting real tags, at which point the `require` lines above would point at a real
version and the `replace` directives could be dropped for a downstream install.

Even if that were fixed, `go install` still cannot pass the `-ldflags` that stamp
the version, commit, and build date - a go-backend install would present itself
to magus as `unknown (unknown) built unknown`, the dev-build sentinel magus keys
on internally to fingerprint an unstamped build, rather than the release it came
from.
