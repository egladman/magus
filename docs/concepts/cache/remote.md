---
title: Remote caching
description: Wire an S3 or GitHub Actions remote cache provider as a spell so CI runners replay signed build artifacts across machines with a strict trust model.
tags:
  [
    remote-cache,
    cache,
    ci,
    s3,
    github-actions,
    artifacts,
    signing,
    supply-chain,
  ]
aliases: [concepts/remote-cache]
---

# Remote caching

magus's [build cache](../../../README.md#build-model) is content-addressed: a target's
output is keyed by the SHA-256 of its inputs, so an unchanged target replays its
previous output instead of rebuilding. That cache lives on disk (`.magus/` in the
workspace root) and is **local** to one machine.

A **remote cache** shares those artifacts across **CI runners**, machine to
machine. When the local cache misses, magus asks the remote provider for the
artifact; if found, it downloads and replays it instead of building. After a genuine
build, magus uploads the result so the next machine gets a hit. A cold CI runner can
replay work another runner (or `main`) already did.

The remote cache is **CI-only infrastructure**, not for developer laptops, and
magus is built to keep it that way. A cache hit replays another machine's build
outputs into your tree, so whoever can write an artifact a consumer trusts can
inject arbitrary files into that consumer's build. That is a supply-chain trust
boundary, so **every remote artifact must be cryptographically signed by a trusted
key, and wiring a provider without a trust set is refused** (see
[Signing is required](#signing-is-required-trust-model) below). Neither a developer,
a fork PR, nor anyone holding raw bucket credentials can publish an artifact that
any machine will replay.

magus itself knows nothing about S3 or GitHub. A provider is a
[spell](../spells.md) that declares no operations and instead exports the cache
contract: functions the remote-cache subsystem detects by name and invokes:

| function                   | when                              | returns                                                        |
| -------------------------- | --------------------------------- | -------------------------------------------------------------- |
| `enabled(target, cb)`      | once, before any other call       | is the provider configured? (gates everything)                 |
| `get_artifact(target, cb)` | on a local-tier miss              | download into `dest`; `true` = hit, `false` = not stored       |
| `put_artifact(target, cb)` | after a build, and for a backfill | upload `src`; `true` = stored, `false` = already stored        |
| `has_artifact(target, cb)` | before a backfill (optional)      | is it stored? without downloading; absent = cannot say         |
| `prune(target, cb)`        | `config cache prune --remote`     | evict by retention policy (optional); `true` = the sweep ended |

A function that cannot do its job **throws**. A throw is a failure: counted as
failed, never as missed, and it degrades the run to the local tier. A provider must
not return `false` for a failed request, since that would read as a cold cache or as
another writer's entry. Without `has_artifact` the remote tier is not backfilled:
uploading every hit to find out would cost more than the miss it saves.

These are not operations a target composes; they are the contract the remote-cache
subsystem calls. Everything provider-specific (auth, transport) stays in the spell,
in pure Buzz. See [spells.md](../spells.md) and [engines.md](../engines.md).

## Wiring a provider

Wiring has two parts: the magusfile binds the provider (a spell, i.e. code), and
`magus.yaml` declares the trust set that secures it (`cache.remote.trusted_keys`, i.e.
data). The split is deliberate. A trust anchor is declarative config, not build
logic, so it lives in YAML where it can't branch or compute itself. The spell
**self-gates** via `enabled()` on the configuration it was given, so the provider is a
no-op anywhere it isn't configured (e.g. a developer machine with no credentials). It
never gates on detecting where it runs; see
[Told, never guessed](../../doctrine.md#told-never-guessed):

```buzz
// magusfile.buzz
import "spells/github/actions" as github;
magus\cache.remote(github);
```

```yaml
# magus.yaml
cache:
  remote:
    trusted_keys:
      - "<base64 Ed25519 public key>"
```

`magus\cache.remote(handle)` records the provider; magus resolves and drives it
during a run. Bind **one** provider. A non-empty `cache.remote.trusted_keys` is
**required** alongside it: a remote provider with no trust set fails at load (see
the next section). Generate a key with `magus config cache key generate`.

### GitHub Actions Cache

The `github-actions` spell ([`spells/github/actions`](../../../spells/github/actions/spell.buzz))
stores artifacts in the GitHub Actions Cache, over its v2 (Twirp) API.

```buzz
import "spells/github/actions" as github;
magus\cache.remote(github);
```

It reads everything it needs from two variables the runner hands to a JavaScript action,
which the workflow re-exports to its steps
([how](../../guides/integrations/github-actions.md#remote-caching)):

| variable                | provided by       | purpose                            |
| ----------------------- | ----------------- | ---------------------------------- |
| `ACTIONS_RESULTS_URL`   | the workflow step | cache service (v2) base URL        |
| `ACTIONS_RUNTIME_TOKEN` | the workflow step | bearer token for the cache service |

There is no transport to configure: a job that exports them uses the cache, and a job or a
laptop that does not misses every read and stores nothing. You still declare a trust set and set the signing secret as for
any provider (see [Signing is required](#signing-is-required-trust-model)). GitHub
evicts old artifacts on its own (7-day idle / repo size cap).

### S3, MinIO, Cloudflare R2, Backblaze B2

The `aws-s3` spell ([`spells/aws/s3-cache`](../../../spells/aws/s3-cache/spell.buzz))
stores artifacts in any S3-compatible bucket, signing every request with AWS
Signature V4.

```buzz
import "spells/aws/s3-cache" as s3;
magus\cache.remote(s3);
```

Configuration comes from the environment (standard AWS variables plus a bucket):

| variable                | required | purpose                                                                                                     |
| ----------------------- | -------- | ----------------------------------------------------------------------------------------------------------- |
| `MAGUS_S3_BUCKET`       | yes      | bucket name (gates the provider)                                                                            |
| `AWS_ACCESS_KEY_ID`     | yes      | access key (gates the provider)                                                                             |
| `AWS_SECRET_ACCESS_KEY` | yes      | secret key                                                                                                  |
| `AWS_SESSION_TOKEN`     | no       | for temporary credentials                                                                                   |
| `AWS_REGION`            | no       | region (falls back to `AWS_DEFAULT_REGION`, then us-east-1)                                                 |
| `MAGUS_S3_ENDPOINT`     | no       | base URL incl. scheme, no trailing slash; set for MinIO/R2/B2 (default `https://s3.<region>.amazonaws.com`) |

Unlike the GitHub provider, S3 has no automatic eviction. Prune it on a schedule:

```sh
magus config cache prune --remote   # evict by the configured retention policy
```

## Signing is required (trust model)

magus does not trust the store. Every remote artifact carries a detached **Ed25519
signature** over its manifest (which commits to the cache key and to every output
blob's content hash). On import, an artifact is replayed **only if** it is signed by
a key in the configured trust set; an unsigned, untrusted, or tampered artifact is
rejected and the build falls back to a normal local build. The trust is
asymmetric:

- The **public** verification keys live in `magus.yaml` (`cache.remote.trusted_keys`).
  They are not secret. Any machine (CI, a laptop, a fork PR) can verify and so
  still get cache hits.
- The **secret** signing seed lives only in trusted CI, as the
  `MAGUS_CACHE_SIGNING_KEY` environment secret. Only a holder of the seed can
  produce a signature. A machine without it (every machine but trusted CI)
  cannot publish an artifact others will replay; magus won't even attempt
  the upload.

Because verification happens on the consumer, this holds even against an attacker
who bypasses magus entirely and writes poisoned bytes straight into the bucket:
with no valid signature, every consumer rejects them.

**Wiring a remote provider without a trust set is a hard error**, on every machine,
so a shared cache can never come up unverified. _Upgrading an existing remote
cache:_ add `cache.remote.trusted_keys` to `magus.yaml` and set `MAGUS_CACHE_SIGNING_KEY`
on trusted pushes, or the run fails at load with a message saying so.

### Insecure mode (no signing)

`cache.remote.insecure: true` (env `MAGUS_CACHE_REMOTE_INSECURE`) is the explicit
opt-out: the provider runs with **no trust set and no signing key**, importing and
producing **unsigned** artifacts. This removes the supply-chain protection above
(any writer the store trusts can inject files into a consumer's build), so it is
only appropriate for a **trusted single-repo CI** (no fork PRs writing the store)
or for **validating a provider before minting keys**. It is off by default and must
be set deliberately; prefer a signed trust set for anything shared. Setting it is
mutually exclusive with `trusted_keys` in effect: when `insecure` is true,
verification is skipped regardless of any keys.

### Generating and trusting a key

```sh
magus config cache key generate    # mint a keypair; prints the seed, pubkey, keyid
```

It prints, once and never to disk: the secret seed (set it as the
`MAGUS_CACHE_SIGNING_KEY` CI secret), the public key, and a ready-to-paste
`cache.remote.trusted_keys` YAML snippet. Add the public key to `magus.yaml`.

To hand the seed to a secret store without reading it, ask for that one field. The
record is `{keyid, seed, pubkey}`, and `-o template` projects the `-o json` names, so
it is `{{.seed}}` and not `{{.Seed}}`:

```sh
magus config cache key generate -o template='{{.seed}}'   # the seed alone on stdout
```

Everything else moves to stderr in that mode, so a pipe receives the secret and
nothing else. `--tee` is rejected here rather than honored: it mirrors structured
output into a file, and a signing key must not come to rest on disk.

```sh
magus config cache key id <pubkey>   # show the keyid + pubkey for a key
magus config cache key id            # same, derived from MAGUS_CACHE_SIGNING_KEY (seed never printed)
```

**Rotation:** add the new public key to `trusted_keys` alongside the old one, switch
CI's `MAGUS_CACHE_SIGNING_KEY` to the new seed, then drop the old key once no live
artifact was signed by it. Multiple trusted keys are supported for exactly this
overlap.

### Set the signing secret in CI

```yaml
# in your trusted-push workflow only (e.g. push to main) - never exposed to fork PRs
env:
  MAGUS_CACHE_SIGNING_KEY: ${{ secrets.MAGUS_CACHE_SIGNING_KEY }}
```

### Runbook: turning it on for a GitHub repository

Four steps, in this order. The cache stays off until the last one, so a half-finished
setup degrades to local-only rather than breaking a build.

**1 and 2. Mint the key and store it.** Pick one of two custody models. Both keep the
seed off disk; they differ in whether you ever see it.

_Hand it straight to the secret store, unseen._ `-o template='{{.seed}}'` puts the seed
alone on stdout - no banner, and no trailing newline, which matters because
`gh secret set` stores stdin verbatim and one stray byte becomes part of the secret:

```sh
set -o pipefail
magus config cache key generate -o template='{{.seed}}' | gh secret set MAGUS_CACHE_SIGNING_KEY
```

`set -o pipefail` is not optional here. A pipeline reports only its LAST command's
status, so without it a failed keygen still looks successful and `gh` stores whatever
it read - possibly nothing. The keyid and public key are printed to stderr, so you
still see the half you need for step 3.

_See it once, then file it._ Use this when the seed belongs in your own password
manager as well. `gh secret set` reads stdin when given no `--body`, so the value stays
out of your shell history and out of the process list - paste at the prompt, Ctrl-D:

```sh
magus config cache key generate
```

```sh
gh secret set MAGUS_CACHE_SIGNING_KEY
```

Never pass a seed as `--body` or with `echo ... |`; both put it in history. `--tee` is
refused on `key generate` for the same reason - it writes structured output to a file,
and a signing key must not come to rest on disk.

The web UI is equally fine for either model: _Settings -> Secrets and variables ->
Actions -> Secrets -> New repository secret_. A paste into a password field is not in
your shell history either.

**3. Publish the public key.** It is not secret, so an argument is fine here. It goes
in two places - `magus.yaml` is what every consumer verifies against, and the
repository variable is what the workflow hands to `MAGUS_CACHE_REMOTE_TRUSTED_KEYS`:

```sh
gh variable set MAGUS_CACHE_PUBLIC_KEY --body "<the public key from step 1>"
```

Then add the same value under `cache.remote.trusted_keys` in `magus.yaml` and commit it.

**4. Confirm what CI signs with.** This derives the public identity from the seed and
never echoes the seed itself:

```sh
magus config cache key id
```

Run it locally with `MAGUS_CACHE_SIGNING_KEY` exported, or in a CI step, and check the
pubkey it prints matches the one in `magus.yaml`.

**Do not set `MAGUS_CACHE_REMOTE_INSECURE` to enable the cache.** It disables
verification, and because a workspace commonly gates its `cache.remote(...)` wiring on
either variable, setting it can be the only thing turning the cache on - a setup that
looks configured, ships a `trusted_keys` block, and verifies nothing. Let the trust set
be the switch.

## Never write the remote tier from untrusted refs (defense in depth)

The remote cache is the **remote tier**; `.magus/` is the **local tier**. Lookup reads
the local tier, then the remote tier, and each is written independently (see
[Cache tiers](../cache.md#cache-tiers)).

Signatures are the primary defense; keeping untrusted refs off the remote tier is a
complementary one. Even though an unsigned PR push could never replay anywhere, you
can also stop a PR from uploading to the remote tier at all (**replay hits, never
store**) by declaring remote-tier writes off on the event:

```yaml
# in your CI workflow env: false on a pull request, unset elsewhere
MAGUS_CACHE_REMOTE_WRITE_ENABLED: ${{ github.event_name == 'pull_request' && 'false' || '' }}
```

`MAGUS_CACHE_REMOTE_WRITE_ENABLED=false` (config key `cache.remote.write.enabled`)
suppresses every `put_artifact` upload, backfill, published output bundle and
knowledge shard, while the run keeps writing its local tier, so a CI cache step that
saves `.magus` between a PR's pushes still has entries to carry. Leave it unset on
trusted pushes rather than `true`: unset, the remote tier is written whenever the
signing key is present and a failing store degrades the run; `true` makes every
remote write required, so an outage fails the build. To write neither tier, set
`MAGUS_CACHE_WRITE_ENABLED=false` instead; `true` for the remote tier with the local
tier off is a config error. See
[Why a pull request cannot poison the default branch](#why-a-pull-request-cannot-poison-the-default-branch).

A remote-tier lookup that finds nothing prints
`<project> not in the remote cache (out...)`, naming the ref the producing run
printed for that key; a hit prints `(cached from <backend>, <size>, ...)`; and the
end-of-run line counts restored, missed, stored and failed. At `-v` the miss also
carries one digest per key-input class, masked as stored key inputs are, so two
machines that should share an entry show which class differs.

## Why a pull request cannot poison the default branch

In the 2026 TanStack compromise, a `pull_request_target` workflow ran a fork's code
with the base repository's cache scope.[^tanstack-2026] That code saved a pnpm store
under the key the default branch's release workflow computes. The release workflow
restored it and published 84 malicious packages.

The attack needed untrusted code that can write a cache entry, a trusted ref that
restores it, and a restore that never checks the writer. This repository's CI denies
each one:

<!--diagram:cache-trust-->

- **Pull requests do not write the remote tier.** `ci.yaml` sets
  `MAGUS_CACHE_REMOTE_WRITE_ENABLED=false` on `pull_request` events and hands
  `MAGUS_CACHE_SIGNING_KEY` only to runs on the default branch.
- **A pull request's local tier stays with it.** The workflow saves `.magus/` on
  `pull_request` events only, and GitHub scopes that save to the pull request's ref,
  out of the default branch's reach. No workflow uses `pull_request_target`, and the
  default-branch job that checks out pull request code saves no cache.
- **Every remote read verifies a signature.** An entry that reached the store any
  other way carries no signature from `cache.remote.trusted_keys`, so the consumer
  rejects it and rebuilds.

The first two are workflow configuration. The third lives in magus and holds whatever
the workflow says, unless you turn on [insecure mode](#insecure-mode-no-signing).

[^tanstack-2026]: TanStack, "Postmortem: TanStack npm supply-chain compromise",
    <https://tanstack.com/blog/npm-supply-chain-compromise-postmortem>. `bundle-size.yml`
    saved the poisoned entry on 2026-05-11 and `release.yml` restored it the same day.

## Observability

When [telemetry](../telemetry.md) is enabled, magus instruments every remote
`get`/`put` automatically, with no provider changes, since the wrapping
happens around the `RemoteBackend` interface, not inside the spell. You get the
`magus.cache.remote.{hits,misses,errors,duration,io.size}` metrics (hit-rate,
latency, bytes moved) plus a `magus.cache.remote.get`/`.put` span per operation,
so a slow fetch or upload shows up inline in the build trace. Remote metrics live
under their own `.remote` prefix and are never folded into the local
`magus.cache.*` counters. See the
[telemetry reference](../telemetry.md#remote-cache) for the full instrument list.

## Writing your own provider

Any store reachable over HTTP can be a provider. Implement the contract functions
(`enabled`/`get_artifact`/`put_artifact`, and `has_artifact` so the remote tier can be
backfilled) in a spell: read inputs from the `cb(io)` callback (`io.hash`,
`io.dest`/`io.src`), use the `http` byte primitives
(`http\download`/`upload_chunked`/`byteSize`, `http\request("HEAD", ...)` for
`has_artifact`) and `crypto` for request signing (e.g. AWS SigV4 via
`crypto\hmacSha256`), return the boolean result, and throw on a failed request. The two
shipped providers are worked examples; start from whichever transport is closest.

A provider is a pure byte mover: **artifact signing and verification happen in
magus's core, not in the spell.** A provider never sees, produces, or checks a
cache-artifact signature, so it cannot weaken or bypass the trust model and it gets
signing for free. It only moves the opaque bytes.
