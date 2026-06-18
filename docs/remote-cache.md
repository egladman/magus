# Remote caching

magus's [build cache](../README.md#build-model) is content-addressed: a target's
output is keyed by the SHA-256 of its inputs, so an unchanged target replays its
previous output instead of rebuilding. That cache lives on disk (`.magus/` in the
workspace root) and is **local** to one machine.

A **remote cache** shares those artifacts across **CI runners** — machine to
machine. When the local cache misses, magus asks the remote backend for the
artifact; if found, it downloads and replays it instead of building. After a genuine
build, the result is uploaded so the next machine gets a hit. A cold CI runner can
replay work another runner (or `main`) already did.

The remote cache is **CI-only infrastructure**. It is not for developer laptops,
and magus is built to keep it that way: a cache hit *replays another machine's
build outputs into your tree*, so whoever can write an artifact a consumer trusts can
inject arbitrary files into that consumer's build. That is a supply-chain trust
boundary, so **every remote artifact must be cryptographically signed by a trusted
key, and wiring a backend without a trust set is refused** (see
[Signing is required](#signing-is-required-trust-model) below). A developer — or
a fork PR, or anyone holding raw bucket credentials — cannot publish an artifact that
any machine will replay.

magus itself knows nothing about S3 or GitHub — a backend is an ordinary
[spell](spells.md) exposing three [function-ops](spells.md#operations-come-in-two-shapes):

| op                      | when                          | does                                           |
| ----------------------- | ----------------------------- | ---------------------------------------------- |
| `enabled(target, cb)`   | once, before fetch/push       | is the backend active here? (gates everything) |
| `get_artifact(target, cb)` | on a local cache miss         | download the artifact into `dest`; `true` = hit   |
| `put_artifact(target, cb)` | after building a missed artifact | upload the artifact at `src`; `true` = stored     |

Everything backend-specific (auth, transport) stays in the spell, in pure Buzz —
see [spells.md](spells.md) and [engines.md](engines.md).

## Wiring a backend

Wiring has two parts: the magusfile binds the backend (a spell, i.e. code), and
`magus.yaml` declares the trust set that secures it (`cache.remote.trusted_keys`, i.e.
data). The split is deliberate — a trust anchor is declarative config, not build
logic, so it lives in YAML where it can't branch or compute itself. The spell
**self-gates** via `enabled()`, so the backend is a no-op anywhere it isn't
configured (e.g. a developer machine with no credentials):

```buzz
// magusfile.buzz
import "spells/github/actions" as github;
magus.cache.remote(github);
```

```yaml
# magus.yaml
cache:
  remote:
    trusted_keys:
      - "<base64 Ed25519 public key>"
```

`magus.cache.remote(handle)` records the backend; magus resolves and drives it
during a run. Bind **one** backend. A non-empty `cache.remote.trusted_keys` is
**required** alongside it — a remote backend with no trust set fails at load (see
the next section). Generate a key with `magus config cache key generate`.

### GitHub Actions Cache

The `actions` spell ([`spells/github/actions`](../spells/github/actions/spell.buzz))
stores artifacts in the GitHub Actions Cache, over its v2 (Twirp) API.

```buzz
import "spells/github/actions" as github;
magus.cache.remote(github);
```

It reads everything it needs from the runner environment, all provided
automatically inside a GitHub Actions job:

| variable                | provided by | purpose                                    |
| ----------------------- | ----------- | ------------------------------------------ |
| `GITHUB_ACTIONS`        | the runner  | gates the backend (`"true"` only in a job) |
| `ACTIONS_RESULTS_URL`   | the runner  | cache service (v2) base URL                |
| `ACTIONS_RUNTIME_TOKEN` | the runner  | bearer token for the cache service         |

There is no *transport* to configure: bind it, and it activates in CI and stays
dormant locally. You still declare a trust set and set the signing secret as for
any backend (see [Signing is required](#signing-is-required-trust-model)). GitHub
evicts old artifacts on its own (7-day idle / repo size cap).

### S3, MinIO, Cloudflare R2, Backblaze B2

The `s3-cache` spell ([`spells/aws/s3-cache`](../spells/aws/s3-cache/spell.buzz))
stores artifacts in any S3-compatible bucket, signing every request with AWS
Signature V4.

```buzz
import "spells/aws/s3-cache" as s3;
magus.cache.remote(s3);
```

Configuration comes from the environment (standard AWS variables plus a bucket):

| variable                | required | purpose                                                                                                      |
| ----------------------- | -------- | ------------------------------------------------------------------------------------------------------------ |
| `MAGUS_S3_BUCKET`       | yes      | bucket name (gates the backend)                                                                              |
| `AWS_ACCESS_KEY_ID`     | yes      | access key (gates the backend)                                                                               |
| `AWS_SECRET_ACCESS_KEY` | yes      | secret key                                                                                                   |
| `AWS_SESSION_TOKEN`     | no       | for temporary credentials                                                                                    |
| `AWS_REGION`            | no       | region (falls back to `AWS_DEFAULT_REGION`, then us-east-1)                                                  |
| `MAGUS_S3_ENDPOINT`     | no       | base URL incl. scheme, no trailing slash — set for MinIO/R2/B2 (default `https://s3.<region>.amazonaws.com`) |

Unlike the GitHub backend, S3 has no automatic eviction. Prune it on a schedule:

```sh
magus config cache prune --remote   # evict by the configured retention policy
```

## Signing is required (trust model)

magus does not trust the store. Every remote artifact carries a detached **Ed25519
signature** over its manifest (which commits to the cache key and to every output
blob's content hash). On import, an artifact is replayed **only if** it is signed by
a key in the configured trust set; an unsigned, untrusted, or tampered artifact is
rejected and the build falls back to a normal local build. The trust is
asymmetric and that is the whole point:

- The **public** verification keys live in `magus.yaml` (`cache.remote.trusted_keys`).
  They are not secret. Any machine — CI, a laptop, a fork PR — can *verify* and so
  still get cache hits.
- The **secret** signing seed lives only in trusted CI, as the
  `MAGUS_CACHE_SIGNING_KEY` environment secret. Only a holder of the seed can
  *produce* a signature. A machine without it (every machine but trusted CI)
  literally cannot publish an artifact others will replay — magus won't even attempt
  the upload.

Because verification happens on the consumer, this holds even against an attacker
who bypasses magus entirely and writes poisoned bytes straight into the bucket:
with no valid signature, every consumer rejects them.

**Wiring a remote backend without a trust set is a hard error**, on every machine,
so a shared cache can never come up unverified. *Upgrading an existing remote
cache:* add `cache.remote.trusted_keys` to `magus.yaml` and set `MAGUS_CACHE_SIGNING_KEY`
on trusted pushes, or the run fails at load with a message telling you exactly
that.

### Insecure mode (no signing)

`cache.remote.insecure: true` (env `MAGUS_CACHE_REMOTE_INSECURE`) is the explicit
opt-out: the backend runs with **no trust set and no signing key**, importing and
producing **unsigned** artifacts. This removes the supply-chain protection above —
any writer the store trusts can inject files into a consumer's build — so it is
only appropriate for a **trusted single-repo CI** (no fork PRs writing the store)
or for **validating a backend before minting keys**. It is off by default and must
be set deliberately; prefer a signed trust set for anything shared. Setting it is
mutually exclusive with `trusted_keys` in effect — when `insecure` is true,
verification is skipped regardless of any keys.

### Generating and trusting a key

```sh
magus config cache key generate    # mint a keypair; prints the seed, pubkey, keyid
```

It prints, once and never to disk: the secret seed (set it as the
`MAGUS_CACHE_SIGNING_KEY` CI secret), the public key, and a ready-to-paste
`cache.remote.trusted_keys` YAML snippet. Add the public key to `magus.yaml`.

```sh
magus config cache key id <pubkey>   # show the keyid + pubkey for a key
magus config cache key id            # same, derived from MAGUS_CACHE_SIGNING_KEY (seed never printed)
```

**Gold-standard custody:** generate the key inside a one-shot CI bootstrap job and
write the seed straight into your secret store, so it never touches a developer
machine. **Rotation:** add the new public key to `trusted_keys` alongside the old
one, switch CI's `MAGUS_CACHE_SIGNING_KEY` to the new seed, then drop the old key
once no live artifact was signed by it. Multiple trusted keys are supported for
exactly this overlap.

### Set the signing secret in CI

```yaml
# in your trusted-push workflow only (e.g. push to main) — never exposed to fork PRs
env:
  MAGUS_CACHE_SIGNING_KEY: ${{ secrets.MAGUS_CACHE_SIGNING_KEY }}
```

## Read-only on untrusted refs (defense in depth)

Signatures are the primary defense; opening the cache read-only on untrusted refs
is a complementary one. Even though an unsigned PR push could never replay
anywhere, you can also stop a PR from writing the store at all — **replay hits,
never publish** — by gating mutability on the event. The same flag suppresses the
remote `put_artifact` upload:

```yaml
# in your CI workflow env
MAGUS_CACHE_IMMUTABLE: ${{ github.event_name == 'pull_request' }}
```

`MAGUS_CACHE_IMMUTABLE=true` (config key `cache.immutable`) opens the cache
read-only; the default is mutable. See the
[supply-chain note in the README](../README.md#shared-cache-trust-signing-and-read-only-refs).

## Observability

When [telemetry](telemetry.md) is enabled, every remote `get`/`put` is
instrumented automatically — no backend changes needed, since the wrapping
happens around the `RemoteBackend` interface, not inside the spell. You get the
`magus.cache.remote.{hits,misses,errors,duration,io.size}` metrics (hit-rate,
latency, bytes moved) plus a `magus.cache.remote.get`/`.put` span per operation,
so a slow fetch or upload shows up inline in the build trace. Remote metrics live
under their own `.remote` prefix and are never folded into the local
`magus.cache.*` counters. See the
[telemetry reference](telemetry.md#remote-cache) for the full instrument list.

## Writing your own backend

Any store reachable over HTTP can be a backend. Implement the three function-ops
(`enabled`/`get_artifact`/`put_artifact`) in a spell — read inputs from the `cb(io)`
callback (`io.hash`, `io.dest`/`io.src`), use the `magus/extra/http` byte
primitives and `magus/extra/crypto` for *request* signing (e.g. AWS SigV4), and
return the boolean result. The two shipped backends are worked examples; start
from whichever transport is closest.

A backend is a pure byte mover: **artifact signing and verification happen in
magus's core, not in the spell.** A backend never sees, produces, or checks a
cache-artifact signature — it cannot weaken or bypass the trust model, and it gets
signing for free. Just move the opaque bytes.
