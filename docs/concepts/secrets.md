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
order: 12
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
- **Nothing is stored in plaintext.** The magusfile holds a *reference*. The backend holds
  the secret, behind whatever authentication it already enforces.
- **The blast radius of a compromised machine shrinks to what an attacker can unlock**,
  rather than everything you have ever exported or written to a file.
- **Reaching for a credential is recorded.** Every read lands in the invocation journal
  with its reference and provider, so "what did this run touch" has an answer.

This is what makes running integration tests against real infrastructure from a laptop a
reasonable thing to do rather than a thing you get away with. The credential is scoped to
the run that needs it.

magus does not stop a compromised machine, and nothing here claims to. What it removes is
the *standing* exposure - the plaintext file and the inherited variable that are readable
long before and long after the moment they were needed.

> The built-in environment provider still reads environment variables, because a CI
> workflow's `env:` block is the only thing that can reach a repository secret - there,
> the platform owns the secret store and the exposure is already scoped to one job. That
> provider is the CI bridge, not an endorsement of `.env` on a workstation. Locally,
> select a real backend.

## Reading a credential

A magusfile can already read a credential. `os\env("DOCKERHUB_TOKEN")` works, and so
does shelling out to `op read` with [`os\exec`](../reference/buzz/os.md). What neither
can do is tell magus that the value is sensitive.

That distinction is the whole feature. A value resolved through a secret provider is one
magus recognizes on the way out, so it never reaches a run log, a terminal, or the
[output store](cache/output-refs.md) in the clear.

```buzz
final token = magus\secret.read("DOCKERHUB_TOKEN");
os\exec("docker", args: ["login", "docker.io", "-u", user, "--password-stdin"],
    dir: ".", opts: {"stdin": token});
```

If the command prints that token back - and plenty of tools do, in a debug dump or a
failure trace - magus redacts it:

```text
DEBUG: authenticating to ghcr.io with password=***
```

Read the same variable with `os\env` and magus has no reason to protect the result. That
is the seam, and it is deliberate rather than a gap.

## Providers

Where a secret comes from is a provider's job. magus ships one and treats everything
else as a spell.

### The built-in environment provider

With no provider selected, a reference is an environment variable name. This is what CI
needs, because a workflow's `env:` block is the only thing that can read a repository
secret - nothing outside the workflow file can:

```yaml
- name: Log in to image registries
  env:
    DOCKERHUB_USERNAME: ${{ secrets.DOCKERHUB_USERNAME }}
    DOCKERHUB_TOKEN: ${{ secrets.DOCKERHUB_TOKEN }}
  run: magus run image-login:cd
```

An unset or empty variable is an error naming the variable, not an empty string. A blank
credential fails later at whatever consumes it, with an error far from the cause.

### Spell-backed providers

Any other backend is an ordinary spell exposing one handler op, selected the same way a
[CI provider](../guides/integrations/ci.md) or a remote cache backend is:

```buzz
import "spells/onepassword" as secrets;
magus\secret.provider(secrets);

final token = magus\secret.read("Private/DockerHub/token");
```

```buzz
// spells/onepassword/spell.buzz (abridged - the shipped spell also classifies failures)
export fun mgs_getName() > str { return "onepassword"; }

export fun resolve_secret(target: Target, cb: fun(any)) > str {
    var io = {};
    cb(io);
    // Guard before coercing: `"" + null` is the string "null" in Buzz, so a
    // missing ref would otherwise reach `op read op://null`.
    if (io["ref"] == null) { throw "no reference given"; }
    final res = os\exec("op", args: ["read", "op://" + ("" + io["ref"])], dir: ".", opts: {});
    return "" + res["stdout"];
}
```

A declared provider wins over the built-in, so selecting one is how a laptop avoids
exporting tokens into a shell while CI keeps using the environment.

magus ships this one at `spells/onepassword/`, imported by path because a spell that
imports a host module cannot be compiled into the binary. Copy it as the starting point
for any backend with a CLI that prints a secret to stdout.

#### Setup, and being honest about it

This is where a secret provider either lands or dies. "Install a CLI, authenticate it,
wire it up" is three steps more than exporting a variable, and if any of them fails with a
bare exit code nobody comes back. So the whole path is two commands and every failure says
what to type next:

```sh
mise use -g op          # or: brew install 1password-cli
op signin
```

```buzz
import "spells/onepassword" as secrets;
magus\secret.provider(secrets);
```

The spell classifies its own failures rather than surfacing an exit status:

| What went wrong | What magus tells you |
| --- | --- |
| `op` not on PATH | the mise, homebrew, and manual install commands |
| not authenticated | `Run \`op signin\`, or set OP_SERVICE_ACCOUNT_TOKEN for an unattended run` |
| wrong vault/item/field | `op item list --vault <vault>` to find the right one |
| anything else | the CLI's own stderr, trimmed |

For CI or any unattended run, a 1Password service account token avoids the interactive
signin entirely - but note that on a CI runner the platform's own secret store is usually
the simpler answer, and the built-in provider already reads it.

#### Credentials and the sandbox

The sandbox scrubs the environment of every child process down to a small
allowlist (HOME, USER, PATH, locale, and friends), and provider subprocesses are
children like any other. The variables 1Password authentication rides on -
`OP_SERVICE_ACCOUNT_TOKEN` for unattended runs, the `OP_SESSION_*` variables
`eval $(op signin)` exports - are NOT on that allowlist, so exporting them in
your shell is not enough: the `op` child never sees them, and the failure reads
as "not signed in" even though you are. Pass them through explicitly in
`magus.yaml`:

```yaml
sandbox:
  env:
    passthrough:
      - OP_SERVICE_ACCOUNT_TOKEN
      - "OP_SESSION_*"
```

The same applies to any spell that authenticates through the environment - a
cosign signing run needs its `COSIGN_*` variables (and, for keyless OIDC in
GitHub Actions, `ACTIONS_ID_TOKEN_REQUEST_URL`/`ACTIONS_ID_TOKEN_REQUEST_TOKEN`)
declared the same way. If a credentialed tool works in your shell and fails
under magus, the scrub is the first thing to rule out.

There is deliberately **no URI scheme**. `op://`-style references exist in tools that
resolve providers per secret, with no selection step; magus selects once, explicitly, so
a reference does not also have to declare which backend it belongs to. The reference
format is the provider's own - a variable name here, a vault path there - and magus
passes it through without parsing.

## One provider per run, and why there is no fallback chain

A realistic setup has different backends in different places: 1Password on a laptop,
whatever CI can reach in CI, and a production vault a developer cannot read at all. The
question that follows is whether a magusfile should name several providers and let magus
try them in order.

**It should not, and magus deliberately cannot.** One provider is active per run, and a
failed read is an error rather than a reason to try the next backend.

The reason is not simplicity. A fallback chain fails *open*: a locked 1Password vault
silently falls through to a stale environment variable, the read succeeds, and the build
pushes with the wrong credential. That failure is invisible - everything is green - and it
is strictly worse than an error naming the reference and the provider. Secrets are the one
place where "try harder to succeed" is the wrong instinct.

So provider selection is an **environment** decision, made once:

```buzz
// CI reaches nothing but its own environment, and the built-in provider is already
// that, so CI selects nothing at all. A laptop opts into 1Password.
if (os\env("CI") == null) {
    magus\secret.provider(onepassword);
}
```

### Keeping one reference across backends

That leaves the real problem: the same credential is `DOCKERHUB_TOKEN` to the environment
provider and `Private/Docker Hub/credential` to 1Password. If the magusfile hard-codes
either, it is no longer portable.

The fix is that **a reference is logical, and mapping it to a backend address is the
provider's job**:

```buzz
// spells/onepassword/spell.buzz - a house convention, expressed in one place
export fun resolve_secret(target: Target, cb: fun(any)) > str {
    var io = {};
    cb(io);
    if (io["ref"] == null) { throw "no reference given"; }
    final ref = "" + io["ref"];               // "dockerhub-token"
    final res = os\exec("op", args: ["read", "op://Engineering/" + ref + "/credential"],
        dir: ".", opts: {});
    return "" + res["stdout"];
}
```

Now the magusfile says `magus\secret.read("dockerhub-token")` and never learns which
backend served it. Each provider owns its own naming convention, one file per backend, and
the credential a developer cannot reach simply fails with a message naming what it wanted -
which is the correct outcome, not a gap.

This is the same principle as [deriving a registry's auth realm](../guides/tips.md#the-auth-realm-is-not-the-push-path):
magus does not model the vendors, it gives you the place to express them.

> **Note:** magus's own magusfile uses environment-variable names as references, because
> it publishes from CI and the built-in provider is the only one it needs. That is the
> less portable choice, taken knowingly. A workspace that expects several backends should
> use logical names from the start - retrofitting them means touching every call site.

### When a single run genuinely needs two backends

It happens: a build that reads a CI token from the environment and a signing key from a
vault. Handle it inside **one** provider spell that routes on a prefix it defines, rather
than by asking magus for multiple active providers. Routing in a spell is explicit,
testable, and visible in one file; multiple active providers pushes the same decision into
the engine where nobody can see which one answered.

## Resolution is lazy, announced, and bounded

Three rules, and each exists because the alternative is infuriating.

**Nothing resolves until something needs it.** A credential is fetched when a target
calls `magus\secret.read`, never at magusfile evaluation. This matters more than it
sounds: a magusfile that read a secret at the top level would prompt on `magus ls`, on
`magus describe`, on every command in the workspace. Keep reads inside target bodies, and
keep the *act of authenticating* in its own target rather than as a side effect of a
build - see the convention below.

The same rule applies to reporting. `magus run image-registries` lists what a publish
needs and resolves nothing; only `image-registries:cd,verify` - a user explicitly asking
"am I set up" - actually calls the provider. A status table that pops an unlock dialog is
the single most annoying thing this feature could do.

**Every wait is announced before it happens.** A provider that prompts prints first:

```text
secret: waiting on onepassword for "Private/Docker Hub/credential" (timeout 90s)
```

An unexplained biometric prompt in the middle of a build is a trust failure, not a UX
wrinkle - you cannot tell whether magus asked for it or something else on your machine
did. The line names what is waiting, what it wants, and how long it will wait. The
[journal](#what-magus-does-with-a-resolved-value) records the read afterwards; this is the
half you can see while the dialog is on screen.

**No terminal means fail fast, not wait.** With a TTY, magus allows 90 seconds for you to
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
  timeout: 60s              # waiting for a person to complete an unlock
  unattended_timeout: 10s   # waiting for a machine, with no terminal to prompt on
```

Each also has a `MAGUS_SECRET_TIMEOUT` / `MAGUS_SECRET_UNATTENDED_TIMEOUT` environment
variable and a `--secret-timeout` / `--secret-unattended-timeout` flag. Raise the
unattended one if a service-account backend is genuinely slow; raising it to hide a
missing credential just moves the failure later.

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
  magus's. The announcement above is meant to make an unexplained request *suspicious* -
  a paste prompt makes it routine.
- **It has no provenance.** A pasted value came from no provider, so the audit trail
  cannot say which backend served it, and "which credentials did this run touch" loses its
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

### The `-login` convention

Give authentication its own target, named `<area>-login`:

```sh
magus run image-login:cd     # authenticates, and does nothing else
magus run image-build:cd     # builds and pushes, assuming you already did
```

Every property above depends on this split. A build target that authenticates as a side
effect cannot be lazy, cannot be run without credentials, and gives no honest place to
announce a wait. Separating them means the expensive, interactive, privileged step is one
you asked for by name.

## Handing a secret to a container build

`--build-arg` bakes its value into image history, where anyone who pulls the image can
read it. Use a BuildKit secret instead: magus resolves the credential, passes it to buildx
through the environment, and the Dockerfile mounts it at a path that never enters a layer.

```buzz
export fun build(ctx: magus\Context, args: [str]) > void {
    final token = magus\secret.read("Private/Registry/token");
    oci["docker-buildx-build"](ctx.withEnv({"BK_TOKEN": token}), {"args": [
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
  magusfile reads back from `os\exec`, and the command line recorded in the invocation
  journal. They are genuinely separate - the buffered result bypasses the live tap
  entirely, and a quiet capture has no live tap at all - so each is redacted at its own
  point rather than at one shared choke point. The mask is a fixed `***`, so it does
  not leak the value's length.
- **Records the read in the invocation journal.** A `secret` event carries the reference
  and the provider that served it - never the value - so an audit can answer which
  credentials a run reached for and through which backend.
- **Memoizes it per reference.** A provider that shells out is usually invoked once per
  reference rather than once per call site. Usually, not always: two targets resolving
  the same reference at the same moment can both miss the memo and both invoke the
  provider, which for an interactive backend means two unlock prompts.

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

That signature is checked. `Registry` fields carrying secret *references* rather than
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
- **Very short values are not redacted at all, and you are not told.** Below four
  characters, masking every occurrence would shred ordinary output while protecting
  something that was never a credential - so magus declines. The internal report for it
  is not currently wired to anything, so a credential this short is silently
  unprotected. Do not rely on a warning.
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
