---
title: CI
description: Compose a ci target from your magusfile, wire the flow with magus needs, and lock down shared-cache writes with Ed25519 trusted keys.
tags:
  [
    ci,
    ci target,
    affected,
    cache,
    remote cache,
    trusted keys,
    magus needs,
    github actions,
  ]
aliases: [ci]
---

# CI

`ci` is an ordinary magusfile target; magus does not hardcode its steps. Export a `ci` function, wire the flow with `magus\needs`, and magus runs it read-only.

```buzz
export fun ci(ctx: magus\Context, args: [str]) > void {
    // declare the edges you want; independent steps run in parallel
    ctx.needs(preflight, generate, format, lint, build, test);
}
```

## Recommendations

We document this order; we don't enforce it. Chain steps with `magus\needs` where order matters (e.g. `test` depends on `build`).

```mermaid
flowchart TD
    cmd["magus affected ci"] --> preflight
    subgraph targets["Targets (composed in your magusfile)"]
        preflight --> generate
        generate --> format
        format --> lint
        lint --> build
        build --> test
    end
```

## Common pitfalls

Each of these is a way a pipeline goes wrong slowly enough that nobody notices
until it is expensive. magus does not prevent all of them, but it makes each one
either impossible or loud.

### The workflow drifts out from under a release branch

A workflow that names steps directly - build here, test there, lint with these
flags - encodes the shape of the repository at the moment it was written. That
shape drifts, and on a long-lived release branch it drifts unwatched. Months
later a security backport fails CI for reasons unrelated to the patch, and the
cherry-pick grows to carry workflow and infrastructure changes nobody wanted in a
maintenance release. The release now contains CI plumbing that was never reviewed
as part of it.

**magus:** reduce the workflow to one contract and it has nothing left to drift.

```yaml
- run: magus affected ci
```

Everything that varies moves into the magusfile, which is versioned with the code
it builds - and should be. A `v2` branch legitimately builds differently from
`v4`: different toolchain, different steps, possibly different projects. Each
branch describes its own build, and no branch needs another's. The contract is
one line: every branch exports a `ci` target. What it composes is that branch's
business.

### The gate goes green having run nothing

The failure mode a thin workflow could otherwise introduce: a branch falls out of
the contract, `ci` resolves to nothing, and the pipeline passes without gating
anything.

**magus:** this is an error, not a warning. `magus affected ci` refuses to run
when no project in scope declares a `ci` target.

```text
no "ci" target defined in the selected project(s); it is the anchor
"magus affected ci" and "magus affected --plan" key off, so this run
would do nothing
```

### The workflow hardcodes the project list

Sharding is the other place repository shape leaks into a workflow. A matrix
listing projects by name is wrong the moment one is added or removed, and the
branch that notices is whichever one fails next.

**magus:** `magus affected --plan` emits the shard matrix as JSON, so the
workflow consumes a value instead of a list. A branch that gains or loses a
project changes its plan output, not its workflow.

### The gate is a commit hook, so it is not a gate

A pre-commit hook that runs lint or type-checking feels like enforcement. It is
not. It runs on the machine of the person being checked, which means it is
advisory by construction:

- `git commit --no-verify` skips it, and people reach for that when they are in a
  hurry, which is exactly when the check matters.
- A fresh clone has no hooks until someone remembers to install them. Hooks are
  not versioned with the code they guard.
- Commits that never touch a working copy - a web UI edit, a bot, a merge queue,
  an agent - never run one at all.
- It checks the files in front of it, not the state of the branch after the merge.

This is the same mistake as validating in the client and trusting the result.
The check has to run somewhere the person being checked does not control,
or it is a suggestion wearing a gate's clothing. The failure mode is
characteristic: an error reaches the main branch, and now it fails for
_everyone_ - including people whose change had nothing to do with it - until
somebody volunteers to fix a break they did not cause.

**magus:** keep the hook, demote it. Its job is fast feedback while you work, not
admission control. The gate is one line, running where nobody can pass `--no-verify`:

```yaml
- run: magus affected ci
```

Two properties make that a real boundary rather than a second copy of the hook:

**It runs the same target the hook runs.** A hook that invokes `magus run lint`
and CI that invokes `magus affected ci` reach the identical target definition from
the magusfile, versioned with the code. There is no separate CI script to drift
out of sync, so "passes locally, fails in CI" stops being a category of bug.

**It cannot silently check nothing.** `magus affected ci` errors when no project in
scope declares a `ci` target, rather than exiting 0 - see
[the gate goes green having run nothing](#the-gate-goes-green-having-run-nothing).
A gate that can be trivially bypassed and a gate that quietly does nothing fail the
same way; both need to be loud.

For anything derived rather than authored - generated files, formatting, lockfiles -
do not check it in a hook at all. Check for **drift**: regenerate and fail if the
tree changed. That is the next pitfall, and it is the same principle applied to
files instead of commits.

### The merge is the first time a step runs

A pull request runs one workflow. The main branch runs a different one. Somewhere
in the second is a step the first never had - building types, a codegen pass, a
stricter lint - and the first time it executes on a given change is _after_ that
change has already landed.

When it fails, it fails on main, which means it fails for everyone. The author who
broke it has moved on; the next person to pull is the one who finds out. Reverting
is a merge commit and a conversation, where a red PR would have been a rebase.

The subtle part is that the PR gate is not weak. It is passing honestly - it is
just gating a pipeline that is not the one protecting main. Two workflows drift
apart the way any two copies do, and nothing in either file says the other exists.

**The distinction that matters is not PR versus main, it is verification versus
delivery.** Publishing a package, pushing an image, tagging a release, deploying -
those genuinely cannot happen before the merge, and keeping them on main is
correct. But a step that _builds_, _checks_, _generates_, or _type-checks_ can
fail for a reason a pull request could have caught, and every one of those belongs
in front of the merge. If a step can go red for a code reason, running it only on
main means you have chosen to find out late.

A useful test: **could you run the main-branch pipeline against a pull request?**
If the honest answer is no, ask which part is actually delivery. Usually it is a
small tail, and everything before it could have run on the PR all along.

**magus:** both branches run the same contract.

```yaml
- run: magus affected ci
```

There is one `ci` target, composed in the magusfile and versioned with the code.
A PR and a main build reach the identical definition, so there is no second
pipeline to drift.

What legitimately differs between them is **scope and permission, not steps**:

- **Scope** comes from the diff. A PR's affected set is computed against the merge
  base, main's against the previous commit. Different projects, same target.
- **Permission** comes from [charms](../charms.md). Locally a `generate` target
  carries `rw` and writes; CI strips the default charm and the same target becomes a
  drift gate. That is one target with two intents, rather than two pipelines that
  happen to share a name.
- **Trust** is a variable, not a workflow. Gate cache writes on the event, and PRs
  replay the shared cache without publishing to it - while running exactly the same
  steps:

  ```yaml
  MAGUS_CACHE_WRITE_ENABLED: ${{ github.event_name != 'pull_request' }}
  ```

Keep delivery in its own job, gated on the merge, and let it depend on the same
`ci` target rather than re-deriving what to check.

### Generated files pass locally and fail in CI

Codegen that runs locally and rewrites the tree makes a dirty checkout look
clean, so drift ships and CI catches it later - or does not.

**magus:** [charms](../charms.md) make writing and verifying the same target with
different intent. Locally a `generate` target carries `rw` and writes; in CI the
default charm is stripped (`--no-default-charms`) and the same target acts as a
pure drift gate. A target can also set `FailOnDrift` to fail outright when it
leaves the tree dirty.

### A volatile test fails the pipeline

Reruns-until-green is how a suite decays into one nobody trusts, because a real
regression looks the same as a volatile failure from the outside.

**magus:** [volatility](../volatility.md) detection decides statistically from
per-target history rather than by rerunning blindly, and only targets that opt in
with `RetryOnVolatile` are eligible.

### A maintenance branch silently upgrades magus

The same drift as the first pitfall, one layer down: a branch that picks up a
newer magus than it was validated against.

**magus:** pin the version per branch and verify its checksum. Whatever else is
shared between branches, this should not be.

### Sharing CI logic across branches, and what it costs

A related pattern: have a release branch check out `main` for its CI logic, so
workflow fixes never need backporting. It does deliver that, and with thin
workflows you rarely need it.

Know the trade before adopting it. You lose the ability to reproduce what a
release actually passed - re-running `v2`'s pipeline a year later runs today's
logic, not the logic that approved it, which is exactly the property an audit or
a regression hunt wants. It also makes the pinned version ambiguous, since the
branch's magusfile may target a magus the shared workflow no longer installs.

It is also the harder pattern to read: nothing in the branch says where its CI
logic lives, so anyone - or any agent - reading only that branch cannot tell. If
you adopt it, say so in the branch's `AGENTS.md` or `MAGUS.md`.

## Shared cache trust

**Who may write the cache is a trust boundary.** The primary defense is Ed25519 signing: a consumer replays a remote artifact only if it carries a signature from a key in `cache.remote.trusted_keys`. Wiring a remote backend without a trust set is refused.

```yaml
# magus.yaml  -  bind the backend in magusfile.buzz via magus\cache.remote(github)
cache:
  remote:
    trusted_keys:
      - "<base64 Ed25519 public key>" # magus config cache key generate
```

A complementary defense is to open the cache **read-only on untrusted refs**: replay hits but never publish. Gate it on the event so only trusted pushes write, and apply the same rule to any persisted run history (the forecaster and volatility detector read it): restore always, save only from trusted pushes.

```yaml
# PRs replay the cache and see main's history, but write neither
MAGUS_CACHE_WRITE_ENABLED: ${{ github.event_name != 'pull_request' }}
```

To set up a shared cache (GitHub Actions Cache, S3/MinIO/R2/B2, or your own backend) and generate signing keys, see [Remote caching](../cache/remote.md).
