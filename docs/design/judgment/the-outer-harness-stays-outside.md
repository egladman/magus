---
title: The outer harness stays outside
order: 3
description: magus judges and informs, and never transforms what reaches the model; the host owns the loop.
tags: [design, judgment, agents, harness]
---

# The outer harness stays outside

magus is the outer harness: deterministic guides and sensors around an agent
host's loop, never the loop itself ([Prior art and terminology](../../scope.md#prior-art-and-terminology)).
The host is the inner harness. It decides what reaches the model and what a
tool call does, and magus never enters that path.

So magus judges and informs; it does not transform. A guard verdict allows,
advises or denies an action and gives its reason. It never rewrites the
command, the prompt or the reply on the way through. The text an agent loads
from magus (skills, MAGUS.md, the AGENTS.md section) is written ahead of time.
It lives in version control, and people review it like code. Compression,
proofreading and regeneration happen to those files, never at session start and
never per request. magus runs no proxy between an agent and its provider and no step
that edits context in flight.

The reason is ownership. A provider changes how its harness loads context,
caches it and calls tools on its own schedule, and a layer that rewrote text in
flight would break quietly when it did: the same trade as
[The host wiring is yours](the-host-wiring-is-yours.md). It would also make the
text a model saw differ from the text a person reviewed, and then version
control no longer records what the model read.

## Enforced by

- A guard verdict has three outcomes, allow, advise and deny, and none of them
  carries a rewritten command.
- Skills, MAGUS.md and the AGENTS.md section are files in version control.
  Targets regenerate them, and the drift gate checks them.
- [A record of refusals](../refusals.md) lists the proxy and the model adapter
  as not built.

## Good

The guard refuses and names the command to run instead; the agent runs it
itself:

```text
`buzz | head` needs no pipe: `-s` stays quiet until something fails, then prints the diagnostics and the log path.
```

## Bad

```text
hook: rewrite `go test ./...` to `magus run test .` and continue
```

The model believes it ran one command while another ran. The transcript a
person reviews no longer says what happened.

## See also

- [Determinism at chosen points](determinism-at-chosen-points.md)
- [The host wiring is yours](the-host-wiring-is-yours.md)
