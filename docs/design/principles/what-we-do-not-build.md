---
title: What we do not build is the product
order: 26
description: When anyone can generate any feature, the tool is defined by what it refuses; the least code is the most secure code, and some capabilities must not be possible at all.
tags: [design, principle, scope, security]
---

# What we do not build is the product

Anything can be built now. An agent writes a remote session server, a text
editor in the console or a plugin loader in an afternoon, so being able to
build a feature says nothing about whether it should exist. What will tell
magus apart from every other tool is what it leaves out. Sometimes the
anti-feature is the feature.

Security makes the case sharper. Every line is attack surface you maintain
forever, and the most secure code is the code that is not there. Some
capabilities are dangerous enough that the right design is for them to be
impossible, not guarded: nothing in magus lets anyone other than the person at
the keyboard run code on their machine. There is no remote session, no hosted
service a capability depends on, and the console answers on loopback only.
magus runs a repository's magusfiles because running magus in a repository is
the consent to run its code
([ADR 0014](../../decisions/0014-running-magus-is-the-consent.md)), and that is
the whole of it.

The console is where this is under the most pressure, because it is the place
a feature is easiest to add and hardest to say no to. It is not an editor, a
terminal or a diff tool that competes with the good ones that exist; anything it
shows has to give something the CLI cannot.

## Enforced by

- [Capability is not a reason](capability-is-not-a-reason.md): a new capability
  needs a failure it removes.
- [A record of refusals](../refusals.md) keeps every no with what decided it.
- The console is loopback-locked and its CSP reaches only its own daemon
  ([Provider I/O is Buzz](../judgment/provider-io-is-buzz.md)).
- [Scope](../../scope.md#the-line) records that no capability requires a hosted
  service.

## Good

A refusal kept in the record, with the reason a person can check:

```text
| a workspace trust store | not built: running magus in a repository is the consent to run its
  code, and magus never runs that code passively |
```

## Bad

```text
Add `magus serve --remote` so a teammate's agent can run targets on my machine.
```

It is an afternoon to build and a remote code execution path to defend forever,
for a convenience `git push` and CI already give.

## See also

- [Capability is not a reason](capability-is-not-a-reason.md)
- [Delete before you add](delete-before-you-add.md)
- [The standard](../standard.md)
