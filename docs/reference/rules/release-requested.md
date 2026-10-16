---
title: "release-requested: a job holding write paths another fork was refused for, with a release request open"
description: "An advisory by default: it explains, and blocks nothing, on a job holding write paths another fork was refused for, with a release request open."
tags: [guard, rules, release-requested, advise]
---

# release-requested

An advisory by default: it explains, and blocks nothing, on a job holding write paths another fork was refused for, with a release request open.

## What it catches

A job holding write paths another fork was refused for, with a release request open.

## Why

A fork refused for a file a live job holds (MGS3032, MGS3036, MGS3046) files an attention request naming that job and the write paths its diff has not touched. The holder is told once a session, on its own calls and never on a deny, which fork wanted what and the `magus job apply -f <record>` that narrows it. It reads the attention queue, never a diff, and a session with no lease never sees it. Disposing of the request silences it.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"release-requested": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [release-requested]: ...
```

A workspace that sets this rule to `deny` sees `deny [release-requested]: ...` instead.

`magus describe rule release-requested` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
