---
title: magus notify
description: Raise one canonical attention event from plain text or a JSON envelope, and optionally surface it as an operating-system notification.
tags: [cli, magus notify, notifications, events, attention, desktop]
---

# magus-notify

Normalize an attention event and optionally notify the local desktop

## Synopsis

**magus** notify [--outcome \<vocab\>] [--desktop]

## Description

Raise one canonical attention event.

The input is read from stdin and may be plain text or a complete event JSON
envelope. Either way the result is one normalized event, so a caller that
knows nothing about magus's event schema can still raise a well-formed one.

--outcome names the event's outcome vocabulary. --desktop additionally
raises an operating-system notification, which is the part a human notices;
without it the event is recorded and nothing pops up.

## Options

**--desktop**
: Also raise an OS notification

**--outcome** *string*
: Outcome vocabulary for the event

## Examples

*Raise a permission prompt on the desktop*

```sh
printf '%s\n' 'needs approval' | magus notify --outcome permission --desktop
```

*Raise a pre-built event envelope*

```sh
printf '%s\n' '{"outcome":"permission","source":{"kind":"agent"},"message":"needs approval"}' | magus notify --desktop
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

