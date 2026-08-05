---
title: Compatibility
description: What magus promises across versions - the three contracts it keeps, why a magusfile that works today keeps working, how a workspace declares the magus version it needs, and why there is no plan for a 2.0.
tags:
  [
    compatibility,
    versioning,
    semver,
    stability,
    upgrade,
    version-floor,
    deprecation,
    support-window,
  ]
---

# Compatibility

A build tool that breaks its own users has failed at the one thing it exists to
do. magus's compatibility rule is short:

**A magusfile that works today keeps working. There is no plan for a 2.0.**

The rest of this page is what that sentence commits to, what it deliberately does
not cover, and the mechanisms that make it checkable rather than aspirational.

## The three contracts

"Compatibility" is not one promise, because magus has three surfaces with three
different consumers and three different ways to break. Keeping them separate is
what makes each one answerable.

| Contract                  | The consumer                            | How it breaks                            | The mechanism                                       |
| ------------------------- | --------------------------------------- | ---------------------------------------- | --------------------------------------------------- |
| magusfile and `magus.yaml` | your repository                          | a key or function magus no longer accepts | additive-only keys, plus a declared version floor    |
| the CLI surface            | your shell scripts and CI configuration | a removed flag, a changed output format   | a drift-gated `api.lock` snapshot of the whole surface |
| the daemon wire API        | the console, MCP clients, editors        | a removed or repurposed protobuf field    | reserved field numbers, and `buf breaking` in `lint` |

The promise is the same for all three. The mechanisms differ because the ways
they break differ.

## What the promise covers

Within a major version:

- **A magusfile keeps loading.** A `magus\project` key, a host module, a spell
  op, or a `magus\Context` method that magus accepts today is accepted by every
  later release in the same major version.
- **A CLI invocation keeps working.** A flag, a subcommand, or an output format
  that exists today keeps existing, and `-o json` keeps producing a document
  whose existing fields keep their names and meanings.
- **A wire message keeps parsing.** A protobuf field is never renumbered,
  retyped, or repurposed. Removed fields have their numbers reserved forever, so
  a newer server can never hand an older client a field that means something
  different from what it expects.

New things are added, never substituted. That is the whole discipline: a new key
appears beside the old one, a new field takes a new number, a new flag joins the
existing ones. Nothing that worked stops working.

## What the promise does not cover

Three exclusions, each deliberate:

**Forward compatibility.** An older magus is not expected to run a newer
workspace. This is the one people meet first, usually as a confusing error, so it
is worth being precise: it is not a break in the promise, it is the absence of a
promise nobody makes. Go's compatibility guarantee says old code compiles on new
toolchains and says nothing about the reverse, which is exactly why `go.mod`
carries a `go` directive. magus does the same thing with a
[version floor](#the-version-floor), for the same reason.

**Behavior a bug produced.** If magus computed a cache key wrongly, the fix
changes the key. Depending on a defect is not depending on a contract.

**Anything explicitly marked experimental.** A surface documented as experimental
may change or disappear. It has to say so where you meet it, not only in release
notes.

## Before 1.0

magus is pre-1.0, and the promise above starts at 1.0.

This is not a loophole, it is the point of the version number. A permanent
guarantee means a name chosen today is a name kept forever, so the window for
renaming anything closes when 1.0 ships. Pre-1.0 releases use that window: a key
may be renamed, a flag may be dropped, a message may be restructured, and the
changelog says so under **Breaking**.

Practically, that means the run-up to 1.0 includes a deliberate pass over the
whole surface - every `magus\project` key, every CLI flag, every config field -
asking whether each name is one worth keeping forever. Any rename that pass wants
has to happen before 1.0 or never.

## The version floor

A workspace declares the oldest magus that can run it:

```yaml title="magus.yaml"
required_version: ">= 0.4.0"
```

magus checks this before it evaluates a single magusfile, so a too-old binary
reports [MGS1021](../reference/codes/magusfile/MGS1021.md) and names both fixes:
upgrade the binary, or raise the pin in your CI setup step. Without the floor, the
same situation surfaces from wherever the magusfile happened to fail - `import
"xml": module not found` reads like a typo, not like an out-of-date tool.

The floor has to be a declaration rather than something magus derives, because of
who reports the error. **The binary producing the message is the old one.** It
cannot know that `xml` was added later; it has never heard of the version that
added it. A declared minimum is the only thing an old binary can evaluate against
a future it does not know about.

A declaration nobody remembers to update is worthless, so the newer binary keeps
it honest: it does know which release introduced each module and key a workspace
uses, so `magus doctor` reports a floor that is lower than what the workspace
actually requires. The old binary reads the floor; the new binary proves the floor
is accurate.

## Client and daemon

The daemon is the one place where two magus builds meet, and it applies two
different rules to two different kinds of client. This is deliberate, not an
inconsistency.

**The CLI must match the daemon exactly.** Forwarding a run to the daemon means
that daemon executes your build logic, so anything short of identical builds risks
running the wrong bytes. The gate is string equality on build identity, and it
fails closed: an unstamped dev build is fingerprinted from its VCS stamp, and a
dirty tree gets a per-process token that can never match anything. Two dirty trees
at the same revision are not provably the same code, so they refuse each other.

**The console is a wire client, and follows the wire contract.** It reads status,
graphs, and activity - it does not hand the daemon code to run - so exact-match
would be absurd: every magus upgrade would blank the browser until you found the
right refresh. Instead the protobuf contract applies, an older console keeps
working against a newer daemon, and the console compares the build it was compiled
against with the one the daemon reports. When the daemon is newer it offers a
reload rather than silently rendering a stale view. This matters more than it
sounds: the console is a PWA whose service worker will happily serve a bundle from
months ago.

The support window for the wire API is the current release and the one before it.
Anything older is asked to upgrade rather than accommodated.

## Deprecation

Nothing is removed within a major version, so deprecation means "there is a better
way now", never "this stops working soon".

A deprecated surface keeps working, says what replaced it at the point of use
rather than only in a document, and is listed in the changelog under
**Deprecated**. Removal waits for a major version, and since there is no plan for
a 2.0, the honest reading is that a deprecated surface stays until there is a
reason strong enough to justify the first one.

## Why no 2.0

A major version bump moves the cost of a design mistake onto everyone using the
tool. That is the wrong direction for a build tool, whose entire value is being
the boring, dependable thing underneath everything else. Go has held one major
version since 2012 and treats a 2.0 as effectively out of the question; the
constraint is what produced the stability, not a consequence of it.

The cost is real and worth naming: every addition is permanent, so the bar for
adding surface is high, and some things stay slightly wrong forever because
fixing them would cost more than living with them. That trade is made on purpose.
An occasional awkward name is cheaper than a migration every user has to perform.

## See also

- [Breaking changes](../migrating/breaking-changes.md): the mechanisms magus gives
  _you_ for your own contracts - `buf-breaking` and a drift-gated `api.lock`.
- [MGS1021](../reference/codes/magusfile/MGS1021.md): the workspace requires a
  newer magus than the one running.
- `magus version`: the running build's version, commit, and build date.
- `magus self update`: move to a newer release.
