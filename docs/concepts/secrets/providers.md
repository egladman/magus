---
title: Secret providers
description: Where a credential comes from - the built-in environment provider, a spell-backed one, and why magus keeps exactly one active per run instead of a fallback chain.
tags:
  [
    secrets,
    secret-provider,
    onepassword,
    vault,
    spells,
    extension,
  ]
---

# Secret providers

Where a secret comes from is a provider's job. magus ships the built-in environment
provider plus two spells, and treats everything else as a spell you write.

## The built-in environment provider

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

## Spell-backed providers

Any other provider is an ordinary spell exposing one handler op, selected the same way a
[CI provider](../ci/providers.md) or a remote cache provider is:

```buzz
import "spells/onepassword" as secrets;
magus\secret.provider(secrets);

final token = magus\secret.read("Private/DockerHub/token");
```

```buzz
// spells/onepassword/spell.buzz
export fun mgs_getName() > str { return "onepassword"; }

export fun resolve_secret(target: Target, cb: fun(any)) > Secret {
    var io = {};
    cb(io);
    final v = proc\exec("op", args: ["read", "op://" + ("" + io["ref"])]).stdout;
    return Secret{ value = v };
}
```

`Secret` comes from `magus/spell`, alongside `Target` and `Command`. The return is typed
because Buzz checks function signatures: a provider returning the wrong shape fails to
load rather than failing at the first read. That is worth having here and not on
`magus\secret.read`, which hands a magusfile a plain `str` - Buzz does not check
host-call results, so a type there would be decoration rather than a constraint.

A provider that returns a bare string still works, so an older spell keeps loading, but
write new ones with the typed return.

A declared provider wins over the built-in, so selecting one is how a laptop avoids
exporting tokens into a shell while CI keeps using the environment.

magus ships this one at `spells/onepassword/`, imported by path because a spell that
imports a host module cannot be compiled into the binary. Copy it as the starting point
for any provider with a CLI that prints a secret to stdout.

### The GitHub Actions provider

magus also ships `spells/github/actions`, the same spell that carries the Actions cache
provider and CI provider. Wire it as a third contract:

```buzz
import "spells/github/actions" as github;

if (os\env("GITHUB_ACTIONS") == "true") {
    magus\secret.provider(github);
}
```

It exists because an Actions secret is **write-only**. Nothing running inside a job can
fetch one; interpolating it into a step's `env:` block is the only path there has ever
been, and the built-in environment provider already reads that. So this provider does the
two things a platform-neutral one structurally cannot.

**It mints OIDC tokens.** A reference prefixed `oidc:` is an audience, and magus requests
a short-lived token from the runner's own endpoint:

```buzz
final token = magus\secret.read("oidc:sts.amazonaws.com");
```

This is the only credential on a runner that is genuinely fetched rather than injected,
which is what lets a repository hold no long-lived cloud key at all. It needs the
permission, which is off by default:

```yaml
permissions:
  id-token: write
```

A job without it is given no endpoint, and the error says so rather than naming an unset
variable.

**It reads an injected variable, and says which line to add when one is missing.** A bare
reference is an environment variable name, as with the built-in provider, but the failure
is a workflow edit rather than a shell one, so the error is the snippet to paste:

```text
github-actions: $DOCKERHUB_TOKEN is not set in this step's environment.
  An Actions secret is only readable through the workflow file - nothing
  running inside the job can fetch one. Add it to the step:
      env:
        DOCKERHUB_TOKEN: ${{ secrets.DOCKERHUB_TOKEN }}
```

Every value it resolves is also registered with the runner via `::add-mask::`, so GitHub
masks it in **every** step's log rather than only in the output magus captures. The
runner already does this automatically for anything interpolated from `secrets.*`; it
does not for a step output injected into `env:`, and it cannot for a token minted
mid-job, which is where this earns its place.

That command necessarily carries the secret in the clear, so it is gated on running under
Actions. The gate is an ordinary environment variable and therefore spoofable, which is
safe only because of an invariant the spell states and future changes must keep: every
value that reaches it was derived from the same environment the gate reads, so whoever
can spoof it can only be shown what they already supplied.

### Setup, and being honest about it

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

| What went wrong        | What magus tells you                                                       |
| ---------------------- | -------------------------------------------------------------------------- |
| `op` not on PATH       | the mise, homebrew, and manual install commands                            |
| not authenticated      | `Run \`op signin\`, or set OP_SERVICE_ACCOUNT_TOKEN for an unattended run` |
| wrong vault/item/field | `op item list --vault <vault>` to find the right one                       |
| anything else          | the CLI's own stderr, trimmed                                              |

For CI or any unattended run, a 1Password service account token avoids the interactive
signin entirely - but note that on a CI runner the platform's own secret store is usually
the simpler answer, and the built-in provider already reads it.

There is deliberately **no URI scheme**. `op://`-style references exist in tools that
resolve providers per secret, with no selection step; magus selects once, explicitly, so
a reference does not also have to declare which provider it belongs to. The reference
format is the provider's own - a variable name here, a vault path there - and magus
passes it through without parsing.

## One provider per run, and why there is no fallback chain

A realistic setup has different providers in different places: 1Password on a laptop,
whatever CI can reach in CI, and a production vault a developer cannot read at all. The
question that follows is whether a magusfile should name several providers and let magus
try them in order.

**It should not, and magus deliberately cannot.** One provider is active per run, and a
failed read is an error rather than a reason to try the next provider.

The reason is not simplicity. A fallback chain fails _open_: a locked 1Password vault
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

### Keeping one reference across providers

That leaves the real problem: the same credential is `DOCKERHUB_TOKEN` to the environment
provider and `Private/Docker Hub/credential` to 1Password. If the magusfile hard-codes
either, it is no longer portable.

The fix is that **a reference is logical, and mapping it to a provider address is the
provider's job**:

```buzz
// spells/onepassword/spell.buzz - a house convention, expressed in one place
export fun resolve_secret(target: Target, cb: fun(any)) > Secret {
    var io = {};
    cb(io);
    final ref = "" + io["ref"];               // "dockerhub-token"
    final v = proc\exec("op", args: ["read", "op://Engineering/" + ref + "/credential"],
        dir: ".", opts: {}).stdout;
    return Secret{ value = v };
}
```

Now the magusfile says `magus\secret.read("dockerhub-token")` and never learns which
provider served it. Each provider owns its own naming convention, one file per provider, and
the credential a developer cannot reach simply fails with a message naming what it wanted -
which is the correct outcome, not a gap.

This is the same principle as [deriving a registry's auth realm](../../guides/tips.md#the-auth-realm-is-not-the-push-path):
magus does not model the vendors, it gives you the place to express them.

> **Note:** magus's own magusfile uses environment-variable names as references, because
> it publishes from CI and the built-in provider is the only one it needs. That is the
> less portable choice, taken knowingly. A workspace that expects several providers should
> use logical names from the start - retrofitting them means touching every call site.

### When a single run genuinely needs two providers

It happens: a build that reads a CI token from the environment and a signing key from a
vault. Handle it inside **one** provider spell that routes on a prefix it defines, rather
than by asking magus for multiple active providers. Routing in a spell is explicit,
testable, and visible in one file; multiple active providers pushes the same decision into
the engine where nobody can see which one answered.
