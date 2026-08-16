---
title: Secrets
description: Resolve credentials through a secret provider so magus knows a value is sensitive and redacts it from the run log, the terminal and the output store - with the built-in environment provider, spell-backed providers, and the limits stated plainly.
tags:
  [
    secrets,
    secret,
    credentials,
    provider,
    redaction,
    scrubbing,
    tokens,
    registry,
    ci,
  ]
---

# Secrets

## Why this exists

The default way developers hold credentials is a `.env` file and an exported shell
variable, and both are worse than they look.

An exported variable is inherited by **every** process you launch afterwards, for as long
as that shell lives. Not just the command that needs it - the package manager, its
lifecycle scripts, your editor's language server, whatever a dependency decided to run at
install time. That is the shape of a supply-chain attack: the malicious postinstall does
not have to steal anything clever, it just reads its own environment. A `.env` file is the
same exposure with a longer half-life, sitting in plaintext on disk, surviving reboots,
occasionally surviving into a git history.

Neither is a mistake anyone makes out of carelessness. They are what the tooling made
easy, and the failure modes are the boring ones: a variable you forgot to unset, a file
you meant to delete, a `.gitignore` entry that was added one commit too late.

A secret provider changes the shape of the exposure, and it is worth being precise about
how:

- **The value exists for one read, in one process.** It is fetched when a target asks for
  it and handed to the one subprocess that needs it, over stdin. It is not in your shell,
  so nothing you launch later inherits it.
- **Nothing is stored in plaintext.** The magusfile holds a _reference_. The provider holds
  the secret, behind whatever authentication it already enforces.
- **The blast radius of a compromised machine shrinks to what an attacker can unlock**,
  rather than everything you have ever exported or written to a file.
- **Reaching for a credential is recorded, and readable.** Every read lands in the run's
  journal with its reference and provider, and `magus query invocation <id> --secrets`
  reads it back, so "what did this run touch" has an answer you can actually get.

This is what makes running integration tests against real infrastructure from a laptop a
reasonable thing to do rather than a thing you get away with. The credential is scoped to
the run that needs it.

magus does not stop a compromised machine, and nothing here claims to. What it removes is
the _standing_ exposure - the plaintext file and the inherited variable that are readable
long before and long after the moment they were needed.

> The built-in environment provider still reads environment variables, because a CI
> workflow's `env:` block is the only thing that can reach a repository secret - there,
> the platform owns the secret store and the exposure is already scoped to one job. That
> provider is the CI bridge, not an endorsement of `.env` on a workstation. Locally,
> select a real provider.

## Reading a credential

A magusfile can already read a credential. `os\env("DOCKERHUB_TOKEN")` works, and so
does shelling out to `op read` with [`proc\exec`](../reference/buzz/os.md). What neither
can do is tell magus that the value is sensitive.

**So do not use `os\env` for a credential.** With no provider selected,
`magus\secret.read("DOCKERHUB_TOKEN")` reads exactly the same environment variable -
the built-in environment provider _is_ a provider. Same variable, same plaintext, same
line of code. The only difference is that magus now knows the value is a credential and
keeps it out of everything it writes down. Reading it with `os\env` gives that up and
buys nothing. Keep `os\env` for configuration that is not sensitive.

That distinction is the whole feature. A value resolved through a secret provider is one
magus recognizes on the way out, so it never reaches a run log, a terminal, or the
[output store](cache/output-refs.md) in the clear.

```buzz
final token = magus\secret.read("DOCKERHUB_TOKEN");
proc\exec("docker", args: ["login", "docker.io", "-u", user, "--password-stdin"],
    dir: ".", opts: {"stdin": token});
```

If the command prints that token back - and plenty of tools do, in a debug dump or a
failure trace - magus redacts it:

```text
DEBUG: authenticating to ghcr.io with password=***
```

Read the same variable with `os\env` and magus has no reason to protect the result -
provenance is what marks a value as a credential, not its name. That seam is deliberate
rather than a gap, and it is exactly why the paragraph above says to stop reaching for
`os\env` here.

## Secrets in a spell op

`magus\secret.read` only works inside a magusfile target body - it is a function call,
and a command op has no body. A spell op's `Command` is static data (`bin`, `args`,
`charms`), resolved once and cached, so it can be charm-patched and previewed by `magus
describe` without running anything. There is no point in that shape for a function call
to land.

That is a real gap for two cases: a command op that genuinely needs a credential (`npm
publish`, a signed release upload), and a **provided** project - one a workspace provider
declared, with no magusfile at all, so there is no target body to call `magus\secret.read`
from in the first place.

`Command.secrets` closes both. It declares the environment the command needs, as env var
name to provider reference - the same kind of reference `magus\secret.read` takes, just
carried as data instead of passed to a function:

```buzz
export fun mgs_listTargets() > any {
    return {"publish": Command{
        bin = "npm",
        args = ["publish"],
        //        env var npm reads -> reference the provider resolves
        secrets = {"NPM_TOKEN": "op://engineering/npm-registry/publish-token"},
    }};
}
```

The two sides are different kinds of name and the example keeps them visibly
different on purpose. The **key** is the environment variable the command itself
looks for, fixed by whatever you are running (`npm` wants `NPM_TOKEN`). The **value**
is a reference in your provider's own addressing - an `op://` path here, a bare
variable name under the built-in environment provider. Writing the same string on both
sides happens to work under the built-in provider and teaches the wrong thing: it makes
the mapping look like a redundant restatement rather than the translation it is.

Each reference is resolved through the workspace's selected provider **at spawn**, the
moment before the command runs, and injected into **that one child process's**
environment - never into `args`, never into a sibling op's environment, never into the
declaration itself. The declaration only ever holds the reference; the value never does.
Resolution goes through the same resolver `magus\secret.read` uses, so the value is
redacted from captured output exactly the same way, and it is subject to the same
[limits](#limits) - short values, encodings other than the ones magus knows, output that
straddles a write boundary.

A charm cannot touch `secrets`. Charms patch `args` - the argv a run prints and a reader
compares - and a charm silently changing which credential a command receives would be the
one kind of edit a diff of the command line could never show.

A **service op** cannot declare secrets, and magus refuses the spell at load rather
than accepting the declaration: a supervised service is started by the daemon's
supervisor, which does not carry a per-op environment yet, so the same op would get
its credential when foregrounded and silently lose it when supervised. Refusing at
load keeps the one rule that matters here: a declared secret is always injected, or
the load fails saying why.

## Providers

Where a secret comes from is a provider's job, and choosing one is an environment
decision rather than a per-read one. See [Secret providers](secrets/providers.md).

## Resolution is lazy, announced, and bounded

Three rules, and each exists because the alternative is infuriating.

**Nothing resolves until something needs it.** A credential is fetched when a target
calls `magus\secret.read`, never at magusfile evaluation. This matters more than it
sounds: a magusfile that read a secret at the top level would prompt on `magus ls`, on
`magus describe`, on every command in the workspace. Keep reads inside target bodies, and
keep the _act of authenticating_ out of a build target's side effects - see
[Authenticating](#authenticating-and-the--login-convention) below for when that means a
separate target and when it just means logging in after the push tells you to.

The same rule applies to reporting. `magus run image-registries` lists what a publish
needs and resolves nothing; only `image-registries:cd,verify` - a user explicitly asking
"am I set up" - actually calls the provider. A status table that pops an unlock dialog is
the single most annoying thing this feature could do.

**Every wait is announced before it happens.** A provider that prompts prints first:

```text
secret: waiting on onepassword for "Private/Docker Hub/credential" (timeout 60s)
```

An unexplained biometric prompt in the middle of a build is a trust failure, not a UX
wrinkle - you cannot tell whether magus asked for it or something else on your machine
did. The line names what is waiting, what it wants, and how long it will wait. The
[journal](#what-magus-does-with-a-resolved-value) records the read afterwards; this is the
half you can see while the dialog is on screen.

**No terminal means fail fast, not wait.** With a TTY, magus allows 60 seconds for you to
answer an unlock. Without one, it allows 10 - because a provider that would prompt cannot,
so it either answers from a cached session immediately or it is going to block until
something else kills it. The error says which situation you are in:

```text
provider "onepassword" needed 10s and there is no terminal to prompt on;
configure an unattended credential for it (a service-account token) or run this interactively
```

Both budgets are configurable, per workspace:

```yaml
# magus.yaml
secret:
  interactive_timeout: 60s  # waiting for a person to complete an unlock
  unattended_timeout: 10s   # waiting for a machine, with no terminal to prompt on
```

Each also has an environment variable and a flag:

| Config key                   | Environment variable               | Flag                           |
| ---------------------------- | ---------------------------------- | ------------------------------ |
| `secret.interactive_timeout` | `MAGUS_SECRET_INTERACTIVE_TIMEOUT` | `--secret-interactive-timeout` |
| `secret.unattended_timeout`  | `MAGUS_SECRET_UNATTENDED_TIMEOUT`  | `--secret-unattended-timeout`  |

Raise the unattended one if a service-account provider is genuinely slow; raising it to
hide a missing credential just moves the failure later.

> This page named the interactive one `timeout` / `MAGUS_SECRET_TIMEOUT` /
> `--secret-timeout` for several releases. None of those ever existed. The flag and the
> variable fail loudly - a parse error, and `magus doctor` reporting an unknown `MAGUS_*`
> name - but an unrecognized **config key** is only a warning, so a `magus.yaml` copied
> from the old text left the budget at its default while looking set. The table above is
> generated from the same source as [the config reference](../reference/config.md); trust
> it over any prose.

That is deliberately not phrased as a timeout. "Timed out" invites a retry; "there is
nobody to ask" tells you to wire a service account. In CI the failure arrives in seconds
rather than at the job's 45-minute limit.

### Why magus will not let you paste a secret instead

Buzz can read stdin, so magus could offer "provider unavailable - paste the value to
continue" during a wait. It deliberately does not, for four reasons that compound:

- **It undoes the point.** The value ends up in your terminal buffer, your scrollback,
  your multiplexer's history, and any screen recording. That is the standing exposure this
  feature exists to remove.
- **It builds a phishing surface.** Once magus is a thing that asks for credentials at a
  prompt, any magusfile can ask for credentials at a prompt that looks exactly like
  magus's. The announcement above is meant to make an unexplained request _suspicious_ -
  a paste prompt makes it routine.
- **It has no provenance.** A pasted value came from no provider, so the audit trail
  cannot say which provider served it, and "which credentials did this run touch" loses its
  answer.
- **It is a fallback**, and fallbacks are already ruled out above: one that succeeds with
  the wrong value is worse than a failure, because everything stays green.

The real need behind the question ("my vault is locked and I just want this build to run")
has a better answer that already works, is explicit, and leaves no prompt surface:

```sh
GHCR_TOKEN=... magus run image-login:cd
```

Scoped to one invocation, no provider selected so the built-in environment provider
serves it, and nothing persists after the process exits.

### Authenticating, and the `-login` convention

Most workspaces should not need a login step at all. Authenticate when the tool tells you
to: run the build, let the push fail, log in, run it again. The re-run is cheap because
everything before the push replays from cache, so being reactive costs a few seconds
rather than a rebuild - and nobody has to know a convention exists in order to get it
right.

That works because the failure teaches the fix. A command op declares `hints`, so the
tool's own error is classified into a next step:

```text
denied: requested access to the resource is denied
hint: the registry accepted you but not this repository; check the image name and that
      the token may push to it
```

The bundled `docker` spell declares these for `docker-buildx`, the op that pushes. Any
spell can, and it is static data like `args` - describable, and not charm-patchable:

```buzz
Command{
    bin = "docker",
    args = ["buildx", "build"],
    hints = [
        Hint{@"match" = "authentication required",
             then = "not authenticated to the registry; run `docker login <registry>` and re-run"},
    ],
}
```

Matching is a plain substring against the tail of the failed command's output, and the
first declared match wins - so order specific before general. See
[Writing a spell](../guides/authoring-spells.md).

Where that is not enough, give authentication its own target named `<area>-login`:

```sh
magus run image-login:cd     # authenticates, and does nothing else
magus run image-build:cd     # builds and pushes, assuming you already did
```

Two situations earn it. **Several registries at once**, where one target authenticates to
all of them and you would otherwise discover them one failure at a time. And an
**unattended runner**, where there is no human to read a hint and you want authentication
as an explicit, ordered step that fails early rather than at the push.

Be clear-eyed about what such a target is. It has no inputs, produces no output, can never
be cached, and mutates ambient state on the machine rather than in your repo - it is a
mode switch that magus runs, not a unit of work. That is a reasonable thing to keep in a
magusfile, and it is not a pattern to reach for by default. If you do keep one, it MUST
declare `skip_cache` with a reason, for the reason the section above gives:

```buzz
"image-login": {"skip_cache": "authenticates to a registry per invocation; a replay would reuse stale credentials"},
```

[MGS1026](../reference/codes/magusfile/MGS1026.md) reports a target that reads a
credential and is still cacheable, so forgetting this is caught rather than discovered as
a login that reports success without authenticating.

### A target that reads a secret must not be cacheable

This is the one limit on this page that can produce a wrong build rather than a leaked
log line, and the `-login` convention above is what makes it dangerous.

**A resolved credential contributes nothing to the cache key.** The key is a function of
the [hashed `Step` fields](cache.md#the-cache-key) - sources, charms, args, allow-listed
env, dependencies, spell version, tool versions - and a value returned by
`magus\secret.read` is none of them. That is the right design: hashing a credential would
write it into cache metadata and partition your cache per rotation. But it has a
consequence you have to handle yourself.

Rotating or revoking a credential invalidates nothing. And an authentication target is
the worst possible shape for that, precisely because the split above made it a good one:
its sources almost never change, so it becomes a permanent cache hit that never contacts
the provider, never authenticates, and **reports success**. The push that follows fails
with the registry's own 401, far from the cause, on a pipeline whose login step is green.

So declare it, with the reason:

```buzz
magus\project({
    "targets": {
        "image-login": {"skip_cache": "authenticates to a registry per invocation; a replay would reuse stale credentials"},
    },
})
```

That is magus's own declaration, verbatim. `skip_cache` takes a reason string rather than
a boolean on purpose - see [Cache](cache.md) - and this is the case the requirement was
written for.

The same applies to any target whose output is a function of a credential, not only a
login: a signed artifact, a fetch from a private registry, a deploy. If revoking the
credential should change what the target produces, the target cannot be replayable.

> A cached artifact built with a credential that was revoked an hour later stays valid
> and replayable, and with a [remote cache](cache/remote.md) it propagates to every
> machine that trusts the pushing key. Nothing about credential validity is an input to
> anything. This is the same trade every content-addressed build system makes; it is
> stated here because the mitigation is yours to apply.

## Secrets and the sandbox

Two documented mechanisms meet here, and the interaction decides whether a command gets
its credential at all.

The [sandbox](sandbox.md) rebuilds a child's environment from an allowlist rather than
inheriting it, and that allowlist deliberately drops exactly the variables a credential
would live in. `Command.secrets` injects a resolved credential into a child's
environment. Both are true, and the order settles it: **scrubbing builds the base
environment, and declared secrets are layered on top of it afterwards.** A secret a
command declares always reaches that command, whatever the passthrough allowlist says,
and it reaches no other process.

The reverse is also worth stating: a credential your magusfile read with `os\env` rather
than through a provider is subject to the allowlist like any other variable, and with the
sandbox enabled it will not reach a subprocess unless you passed it through.

## Handing a secret to a container build

`--build-arg` bakes its value into image history, where anyone who pulls the image can
read it. Use a BuildKit secret instead: magus resolves the credential, passes it to buildx
through the environment, and the Dockerfile mounts it at a path that never enters a layer.

```buzz
export fun build(ctx: magus\Context, args: [str]) > void {
    final token = magus\secret.read("Private/Registry/token");
    docker["docker-buildx"](ctx.withEnv({"BK_TOKEN": token}), {"args": [
        "--secret", "id=registry_token,env=BK_TOKEN",
        "-t", "demo:latest", ".",
    ]});
}
```

```dockerfile
RUN --mount=type=secret,id=registry_token \
    TOKEN="$(cat /run/secrets/registry_token)" && ./fetch-private-dep.sh
```

The argv magus runs carries only `--secret id=registry_token,env=BK_TOKEN` - the flag, not
the value - so nothing sensitive reaches the run log. The child process does receive the
real value, because BuildKit needs it, and if the build echoes it back magus redacts it on
the way out.

## What magus does with a resolved value

- **Redacts it from captured output.** Four paths carry a subprocess's bytes and all
  four are covered: the live stream (terminal), the raw run log, the buffered result a
  magusfile reads back from `proc\exec`, and the command line recorded in the invocation
  journal. They are genuinely separate - the buffered result bypasses the live tap
  entirely, and a quiet capture has no live tap at all - so each is redacted at its own
  point rather than at one shared choke point. The mask is a fixed `***`, so it does
  not leak the value's length.
- **Records the read in the run's journal, where you can read it back.** A `secret` event
  carries the reference and the provider that served it - never the value. Every run has
  an invocation id, shown as `inv:` in `magus query output <ref> --meta`, and that id
  answers the audit question directly:

  ```sh
  magus query invocation <id> --secrets
  ```

  ```text
  inv:     invmsm5rgk21
  command: magus run image-login:cd .
  status:  pass

  secrets: 1 credential read(s)
    14:50:50  . image-login              read secret "GHCR_TOKEN" via onepassword
  ```

  Drop `--secrets` for the whole event stream, or add `-o json` for a record. Run logs are
  trimmed to a cap by the daemon's `RotateLogs` job, so this answers for recent runs
  rather than forever; export what an auditor needs to keep.
- **Memoizes it per reference and per provider.** A provider that shells out is invoked
  once per reference, not once per call site, and two targets resolving the same
  reference at the same moment collapse into a single invocation rather than two unlock
  prompts. The memo is keyed by provider as well as reference, so a magusfile that reads
  before selecting a provider cannot memoize the built-in answer and then serve it under
  a declared one.
- **Holds the value for as long as the workspace is open, not for one run.** That scope
  is deliberate - a magusfile is evaluated once during preload and again during the run,
  and a narrower scope made a single command prompt twice. The cost is that the daemon
  keeps a workspace open for as long as it serves it, so on a machine running `magus
server start` a resolved credential is resident in that process until it restarts, and
  would appear in a heap or core dump. A one-shot CLI invocation holds it for the life of
  that process only. Nothing is written to disk in either case.

## Endpoints: pointing a subprocess at magus instead of the API

`magus\secret.read` hands your magusfile the credential. That works when your own code is
the consumer, because magus knows the value is sensitive and masks it out of the run log,
the terminal and the output store.

It does not work when the consumer is a **subprocess**. A child process holds the value in
its own memory, and magus cannot redact another process. If that child decides at runtime
what to run, or executes code you did not write, its ability to read its own environment
is the exposure.

`magus\secret.endpoint` addresses that one case. It returns a loopback base URL. You point
the child at that instead of the real API, and magus attaches the credential on the way
upstream:

```buzz
object SecretGrant {
    ref: str = "",
    host: str = "",
    header: str = "",
    prefix: str = "",
}

final OPENAI = SecretGrant{
    ref    = "op://vault/openai/key",
    host   = "api.openai.com",
    header = "Authorization",
    prefix = "Bearer ",
};

export fun agent(ctx: magus\Context, args: [str]) > void {
    ctx.skip_cache("reaches for a credential, which contributes nothing to the cache key");
    os\withEnv({
        "OPENAI_BASE_URL": magus\secret.endpoint(OPENAI),
        "OPENAI_API_KEY":  "placeholder-magus-replaces-this",
    }, fun () > void {
        proc\exec("my-agent", args: ["run"], dir: ".");
    });
}
```

You declare the object yourself. magus does not export the type: a type declared in a host
module can be annotated but not constructed, so exporting one would name something you
could not build.

### The placeholder key

openai-python, the Anthropic SDK and most API clients refuse to start without a key, then
set the auth header from it unconditionally. Give them any non-empty string. magus
overwrites that header, because the value you supplied is garbage by construction.

Without the placeholder the SDK exits before it sends anything.

### What the forwarder does

magus binds `127.0.0.1` on a random port and serves one path: a 128-bit token minted per
endpoint. A request presenting that token is forwarded to the granted host over ordinary,
fully verified TLS with the credential attached. Anything else gets a 404.

The token is why a loopback socket is defensible. Without it, any process on the machine
could reach the port and spend your credential by guessing a port number. **Treat the
endpoint URL as a credential**: it belongs in the environment, never in a command line,
since argv is world-readable on Linux. magus masks it out of its own output for the same
reason.

Streaming works. The forwarder does not buffer, so an SSE completion arrives token by
token.

### No interception, and why that is not a compromise

The obvious way to cover every tool at once is to intercept all outbound traffic: generate
a certificate authority, install it in the machine's trust store, terminate each TLS
connection, inject the credential, re-encrypt. magus does not do this.

That mechanism adds surveillance, not security. Decrypting traffic, inspecting it and
re-encrypting it is what a corporate TLS-inspecting VPN does, and the end-to-end guarantee
TLS exists to provide is gone once a third party holds a key that can impersonate every
site you visit.

It also fails its own threat model. A process hostile enough that you must withhold a
credential from it by force is running as you: it can read the CA you just installed, read
magus's memory, or hook the TLS library. You would have added a universal interception
capability to defend against an adversary already inside the boundary it protects.

You have to trust your tooling and your environment somewhere. What magus offers is
narrower: it shrinks the standing exposure so the ordinary failures stop handing the value
over. It is not a containment boundary against code you chose to execute.

### A bypass fails closed

A child that ignores the endpoint and dials the API directly gets a 401, because magus
holds the key. Nothing has to stop it. This is why endpoints need no egress enforcement,
no network sandbox and no per-platform firewall work, and it is the property an allow/deny
egress gate lacks: that fails _open_ the moment something routes around it.

### Scope and lifetime

An endpoint belongs to the run that opened it. Two concurrent runs wanting the same
credential get two sockets and two tokens, so a token leaked from one run authorizes
nothing in another. The listener closes when the run's context ends.

Opening one at a magusfile's top level is refused. A top level runs during `magus ls` and
has no run to end, so the socket would outlive every run and keep one token valid for as
long as the process lives.

### What gets recorded

Opening an endpoint lands in two places. The **run journal**, alongside every credential
read, readable with `magus query invocation <id> --secrets`. The **activity trail**, as a
`credential_grant` event, which is what connects an agent's tool call to the credential it
made spendable.

Both record the reference, the host and the header. Neither records the value: magus
resolves nothing when you declare an endpoint, and resolving one in order to log it would
defeat the point.

### What an endpoint does not do

- **It stops exfiltration, not use.** A child pointed at an endpoint can issue any request
  the declaration permits. It cannot read the credential; it can spend it.
- **It covers what you forward.** A tool with no base-URL setting is not covered, and you
  find that out where you can see it rather than by having magus rewrite its traffic.
- **`Authorization: Basic` is not expressible.** `prefix` writes a literal string before
  the value; it does not base64 a user:password pair.
- **The object is yours, not magus's.** Buzz checks the construction of an object it can
  see, so a misspelled field is a compile error. Field _types_ in an object literal are not
  checked, so `host = 42` compiles and fails when the endpoint opens.

### Why magus does not inject into its own HTTP calls

An earlier version attached these credentials to magus's own `http\*` calls too, matching
each request's host against a registry of declarations.

That got cut. The plaintext sits in magus's memory either way, since the resolver memoizes
it and the redaction set must retain it for masking to work, so withholding it from a Buzz
variable in the same process bought close to nothing over `read`. Meanwhile the host
matching and per-hop redirect re-checking it required produced two credential-leak bugs of
their own: one where a credential followed a redirect into a different declared host
sharing a header name, and one where an `https` to `http` downgrade on the same host kept
the header.

For your own code, `read` is the answer. The subprocess case is the one `read` cannot
serve, and that is what an endpoint is for.

## What a resolved credential is, in Go

Inside magus a resolved credential is a `secret.Value`, not a `string`. Every standard
way of rendering one - `fmt` with any verb, `slog`, JSON - yields `***`, so the plaintext
reaches output only where a caller explicitly asked for it by name with `Reveal()`.

That type exists because redaction at the write boundary cannot be finished. Those
interceptors compare against what `fmt` renders while a handler emits what its _encoder_
produces, and the two differ - a `[]byte` attribute rendered as decimal bytes by `fmt`
was emitted base64-encoded, and decodable, by the JSON handler. No amount of additional
kind-handling closes that, because an interceptor cannot know what a downstream encoder
will do. The value has to mask itself.

Both mechanisms are required, and neither replaces the other. `secret.Value` covers
return values, structured log attributes and descriptor fields. The write interceptors
cover bytes a child process printed, which magus never held as a value.

There are five `Reveal()` call sites in the whole engine, and that is deliberate: they
are the boundaries where a credential stops being self-protecting, and they are meant to
be greppable. If the count grows much past a handful, the boundary is in the wrong place.

## Why a secret is a `str` and not its own type

A reasonable instinct is that `magus\secret.read` should return a distinct `Secret` type
so a credential cannot be mistaken for an ordinary string. It does not, and the reason is
that the type would not be enforced where it matters.

Buzz checks **function signatures** - `fun registries(ctx) > [Registry]` is a real
constraint, and a wrong return type fails the build. It does not check **host call
results**: every module magus exposes (`os`, `fs`, `magus`, ...) is typed as unknown to
the checker, so a `Secret` coming back from `magus\secret.read` would be unknown too.
Every call site would gain a `.value()` unwrap and the checker would verify none of it.

What protects a secret is not its type, it is its **provenance**: magus knows the value is
a credential because it was resolved through the resolver, and that knowledge survives
being assigned, concatenated, and passed to a subprocess - all the things that discard a
type. Redaction keys off having been read, so a wrapper adds ceremony without adding
protection.

Where types DO earn their place is in your own declarations. Keep the registry table an
`object` with named fields and keep the helpers' signatures honest:

```buzz
fun publish_registries(ctx: magus\Context) > [Registry] { ... }
```

That signature is checked. `Registry` fields carrying secret _references_ rather than
values is a convention this page recommends, not a guarantee the compiler makes.

## Limits

These are real, and stating them matters more than the guarantees do: a partial
guarantee described as total changes what people are willing to risk.

- **Only resolved values are known.** A credential a magusfile read with `os\env`,
  bypassing the provider, is invisible to redaction.
- **It is literal substring replacement.** A process that base64-encodes, URL-escapes or
  splits the value defeats it.
- **It cannot see across a write boundary.** A secret straddling two separate writes
  from a child process is redacted only if both halves land in one write.
- **Very short values are not redacted at all.** Below four characters, masking every
  occurrence would shred ordinary output while protecting something that was never a
  credential - so magus declines. It does say so:
  [MGS2011](../reference/codes/sandbox/MGS2011.md) names the reference and the threshold
  at the moment of the read. The value is still unprotected; the warning is the only
  thing magus can offer, and it is on the run log rather than a build failure.
- **magus cannot stop a process from doing what it likes with a value you gave it.**
  Redaction covers what magus captures, not what a tool writes to a file of its own.

Prefer keeping a secret out of an argument list in the first place. magus captures a
command's argv into the run log and the output store, so `--password-stdin` with
`opts.stdin` beats `-p <token>` even with redaction in place.

## See also

- [Output references](cache/output-refs.md) - the durable store redaction protects
- [Tips and tricks](../guides/tips.md) - the declare-once registry-table pattern
- [Writing a spell](../guides/authoring-spells.md) - the full provider contract
- [CI integration](../guides/integrations/ci.md)
