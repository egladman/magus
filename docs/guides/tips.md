---
title: Tips and tricks
description: Non-obvious ways to combine magus subcommands - status sidebars, --step debugging, watch loops, health probes, output field discovery, typed data boundaries, container registry vocabulary and auth realms, and recursive invocation.
tags:
  [
    tips,
    status,
    step,
    watch,
    repl,
    recursion,
    magus\cmd,
    magus status,
    output,
    template,
    fields,
    types,
    secrets,
    registry,
    repository,
    oci,
    harbor,
    ecr,
  ]
---

# Tips and tricks

Non-obvious ways to combine magus subcommands.

## Live pool snapshot in a multiplexer sidebar

`magus status` is a non-blocking, one-shot RPC snapshot: it returns immediately whether the daemon is running or not. Combine `--compact` (a single densely-packed line) with `--watch` to keep a tmux/screen sidebar pane current:

```sh
magus status --compact --watch=15s
```

Sample output:

```text
daemon 3/8 busy · api:build(2.1s) · ui:test(0.5s) · 1 ws
```

When no daemon is running the line reads `daemon: off`, with no error and no hang. Drop `--compact` for the full grid view when you have a wider pane to spare.

The full view also lists workspace locks held by ordinary `magus run` processes,
which may exist without a daemon. When a target is waiting on one, keep this
watch open instead of writing a `sleep`/`ps` loop: it reports the lock holder's
PID, command, directory, age, and waiters. A long run alone is not grounds to
kill it; only act on a verified stale holder.

The same view lists registered shared services with their lifecycle state and
current dependent count, so an idle retained service is not mistaken for active
shared work.

## Step through a target to diagnose a volatile build

`magus run --step` pauses before every subprocess and lets you inspect state, skip commands, or open a REPL mid-run. Concurrency is forced to 1, so commands execute one at a time:

```sh
magus run build --step
magus affected build --step
```

See [`--step`](debugging.md#--step) for the full prompt reference.

## Re-run only affected projects on each save

Pipe `magus watch` into `magus affected --stdin` for a tight inner loop that re-runs only the projects touched by each edit:

```sh
magus watch | while IFS= read -r path; do
    echo "$path" | magus affected --stdin test
done
```

## One-shot daemon health probe

`magus status` exits 0 even when the daemon is down (the pool block reads `daemon: off`). Use it as a cheap, non-blocking reachability probe in scripts or CI health checks, with no risk of hanging on a network timeout:

```sh
magus status
magus status -o json   # machine-readable output
```

## Discover an output's fields for -o json and -o template

Any command that emits structured data documents its own shape. Run it with a bare `-o template` (no template body) and it prints the fields instead of rendering - the json keys usable in both `-o json` and `-o template`, with each field's type and doc. Referenced output types are listed too, so you can drill into a `[]ProjectEntry` without reading source:

```sh
magus describe projects -o template
```

Sample output:

```text
# fields for -o json / -o template (bare -o template lists these):

ProjectsOutput:
  definition  string
  count       int
  projects    []ProjectEntry

ProjectEntry:
  path        string
  spell       string
  depends_on  []string
```

Then write the template (or `jq` filter) against those keys:

```sh
magus describe projects -o template='{{range .projects}}{{.path}}{{"\n"}}{{end}}'
```

The field names are always the json keys - `-o json` and `-o template` share one vocabulary - so `-o json` output doubles as the field reference.

## Where typed data lives: built-in commands versus your targets

The section above is about **built-in commands**. They declare an output type, so `-o json` and `-o template` render real fields and a bare `-o template` lists them. Your own targets are a different shape, and the difference decides how you should structure a magusfile:

| | Built-in command (`magus describe projects`) | Your target (`magus run deploy`) |
| --- | --- | --- |
| Declares an output type | yes, discoverable with bare `-o template` | no |
| `-o json` renders | the command's fields | the run envelope: target, charms, projects, count |
| Domain data reaches you as | typed fields | whatever the target printed, addressable as an [output ref](../concepts/cache/output-refs.md) |
| Signature | n/a | `fun(ctx: magus\Context, args: [str]) > void` |

A target returns `void`. Its result to the outside world is an exit code plus text. So the type system is not absent, it is on the **inside**: helpers can return whatever they like, and only the boundary is untyped.

```buzz
// Typed where it matters. The list never leaves Buzz, so nothing has to parse it.
fun publish_registries(ctx: magus\Context) > [Registry] {
    if (ctx.has_charm("cd")) { return REGISTRIES; }
    return [];
}
```

The practical rule this leads to: **when two steps need to agree on structured data, keep the data in the magusfile and export a verb for each thing you want done with it** - not a target that prints the data for something else to parse.

## Keep structured data in the magusfile, not in the shell

A CI job that publishes images has to log in to exactly the registries it is about to push to. The tempting shape is a target that prints the list and a shell loop that reads it back:

```sh
# Don't. Every consumer re-parses, and the field order is now a contract.
magus run image-registries:cd --silent | while read -r host user token; do
  printf '%s' "${!token}" | docker login "$host" -u "${!user}" --password-stdin
done
```

That crosses the boundary in the worst direction: structured data leaves the type system, becomes whitespace, and gets rebuilt by `read`. Declare the table once and export a verb per action instead:

```buzz
object Registry {
    host: str = "",         // the registry: what `docker login` authenticates against
    repository: str = "",   // the repository reference, never carrying a tag
    user_ref: str = "",     // a SECRET REFERENCE, never the value
    token_ref: str = "",
}

// Look: what will this push to, and am I set up for it?
export fun image_registries(ctx: magus\Context, args: [str]) > void { ... }

// Act: log in to exactly those.
export fun image_login(ctx: magus\Context, args: [str]) > void {
    foreach (reg in publish_registries(ctx)) {
        os\exec("docker", args: ["login", reg.host, "-u", magus\secret.read(reg.user_ref),
            "--password-stdin"], dir: ".", opts: {"stdin": magus\secret.read(reg.token_ref)});
    }
}
```

Why `host` and `repository` are separate fields, and why `user_ref` names a credential instead of holding one, are covered below and in [Secrets](../concepts/secrets.md).

The CI step collapses to one line, and it is the same line you run on a laptop:

```sh
magus run image-login:cd
```

Three properties fall out of this that the shell version does not have:

- **The two halves cannot drift.** `image-login` and `image-build` read the same function, so the set logged into is by construction the set pushed to. Adding a registry is one entry in one list.
- **Selection is by name, not position.** `magus run image-login:cd docker.io` picks one; an unknown host is an error listing the valid ones. Positional indexing would have been worse than it looks - charms change the list length, so index `1` is a registry under one charm and out of range under another.
- **The secret never becomes an argument.** magus captures a command's argv into the run log and output store. Passing `-p <token>` would persist it in both; `opts.stdin` is not captured. The magusfile holds *references*, a [secret provider](../concepts/secrets.md) resolves them, and nothing in between sees a token.

That last point is the boundary worth stating explicitly: **declare the shape in the magusfile, keep the secrets in the environment.** A CI workflow then supplies values for names it did not have to know, and a registry can be added without touching it.

## The auth realm is not the push path

Container registry vocabulary is used loosely everywhere, and the looseness is what makes this next problem hurt. The precise terms, from the [OCI distribution spec](https://github.com/opencontainers/distribution-spec):

| Term | What it is | Example |
| --- | --- | --- |
| **registry** | the server, `host[:port]` | `ghcr.io`, `localhost:5000` |
| **repository** | the namespaced path inside a registry holding one set of related manifests | `egladman/magus`, `library/nginx` |
| **tag** | a *mutable* pointer to one manifest in a repository | `latest`, `v1.2.3` |
| **digest** | the *immutable* content address | `sha256:9f86d0...` |
| **reference** | the whole addressable string | `ghcr.io/egladman/magus:v1.2.3` |
| **image** | strictly the **artifact** - manifest, config, layers | not a string at all |

That last row is the one worth internalizing. An image is a thing in a registry, not its name; the name is a *reference*. "Image" gets used for the reference constantly - Docker's own CLI help says `docker pull NAME[:TAG|@DIGEST]` while its glossary defines an image as a filesystem artifact - so if you name a variable `image` nobody knows which you meant. Name it `reference`, `repository`, or `tag`.

Now the practical problem. **What you authenticate against and what you push to are different strings, and how they differ is per-provider:**

| Provider | `docker login` | push reference |
| --- | --- | --- |
| GHCR / Docker Hub | `ghcr.io` | `ghcr.io/egladman/magus` |
| Harbor | `harbor.example.com` | `harbor.example.com/team-a/app` |
| Amazon ECR | `<acct>.dkr.ecr.<region>.amazonaws.com` | `<acct>.dkr.ecr.<region>.amazonaws.com/myapp` |
| Artifact Registry | `us-central1-docker.pkg.dev` | `us-central1-docker.pkg.dev/proj/repo/app` |

For GHCR and Docker Hub the registry is just the first path segment, so it is easy to believe that is a rule. It is not. Harbor's first path segment is a *project*, and a robot account is frequently scoped to exactly one - so two repositories on one Harbor host can need two different credentials. ECR's registry embeds an account id and a region, and its password is a short-lived token from `aws ecr get-login-password` rather than a stored secret at all.

**magus does not try to model this, and should not.** There is no registry-provider abstraction to get wrong, because the shape is different at every vendor and changes when they change. What magus gives you is the place to compute it:

```buzz
// Split a repository reference into its registry and the rest. The registry is
// everything before the first "/", which is the rule for every provider above -
// the variation is in what the REMAINDER means, not in where the host ends.
fun registry_of(reference: str) > str {
    final parts = reference.split("/");
    return parts[0];
}

// Harbor scopes a robot account per project, so the credential reference has to be
// derived from the project segment rather than declared once for the host.
fun harbor_token_ref(reference: str) > str {
    final parts = reference.split("/");
    return "HARBOR_" + parts[1].upper() + "_TOKEN";
}

// ECR issues a short-lived password instead of storing one. `docker login` still
// takes it on stdin, so nothing downstream changes.
fun ecr_password(region: str) > str {
    return os\exec("aws", args: ["ecr", "get-login-password", "--region", region],
        dir: ".", opts: {}).stdout;
}
```

Then the table declares whatever each entry actually needs, and the login verb reads it:

```buzz
final HARBOR = Registry{
    host = registry_of("harbor.example.com/team-a/app"),
    repository = "harbor.example.com/team-a/app",
    token_ref = harbor_token_ref("harbor.example.com/team-a/app"),
};
```

This is the superpower, and it is the reason the pattern above keeps the data in the magusfile: a registry whose rules nobody anticipated is a **function**, not a feature request. A config format would have to grow a case for Harbor projects, then ECR regions, then whatever comes next. A magusfile just computes it.

Two things to keep straight while you do:

- **A tag is mutable, a digest is not.** Sign and verify by digest. `cosign sign registry/repo@sha256:...` covers exactly the bytes you pushed; signing a tag covers whatever that tag points at right now.
- **Keep the registry and the repository reference as separate fields.** Deriving the registry at the point of use means every call site repeats the split, and the one that forgets sends credentials to the wrong host.

## Interactive debugging entry points

Two entry points into an interactive Buzz REPL, sharing one evaluator:

- **`magus buzz`** - standalone shell with the magusfile loaded.
- **`magus\pry()`** - `binding.pry`-style breakpoint that opens the same REPL mid-target with frame context (`.where`, `.locals`, `.up`/`.down`, `.step`, ...).

```buzz
export fun build(ctx: magus\Context, args: [str]) > void {
    os\exec("go", ["generate", "./..."]);
    magus\pry();   // execution pauses here; inspect or modify state
    os\exec("go", ["build", "./..."]);
}
```

`magus run build --step` pauses before every subprocess instead (concurrency forced to 1) so you can step, skip, or drop into a REPL command-by-command.

Full reference (meta-commands, pry stack navigation, `--step` keymap, multiline behavior) is in [debugging](debugging.md).

## Recursive invocation

Targets can call `magus` recursively. Child invocations forward work to the parent process over a local socket; concurrency limits are shared, so nested calls draw from the same budget instead of each grabbing their own slots.

```buzz
magus\cmd(["run", "build", "api"]);
```

`magus\cmd` is the in-magusfile entry point for invoking magus recursively. When a [daemon](daemon.md) is running, the call rides the existing socket connection instead of spawning a new process.
