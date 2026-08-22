---
title: magus attention
generated_from: internal/clispec/registry.go
description: List the open requests agents raised in this repository - work blocked on input or on approval - and close one by hand.
tags: [cli, magus attention, agents, requests, queue, journal]
---

# magus-attention

List the blocks agents raised and dispose of one

## Synopsis

**magus** attention [ls] [flags]

## Description

List the requests agents have raised in this repository and close one.

A request is opened by \`magus notify\`: an event whose outcome is waiting
(blocked on input) or permission (blocked on approval) becomes a durable request
instead of a notification that scrolls past. Any other outcome opens nothing.

The queue is keyed by repository identity rather than by checkout path, so every
git worktree of one repo lists and disposes the same requests, with no daemon and
no shared branch.

Nothing closes a request automatically. There is no expiry, no severity inference
and no auto-dispose flag, because a request magus could answer by itself would not
have needed a person - see the doctrine page. Re-raising a block that is already
open updates nothing and adds no second row.

### attention dispose options

**--reason** *string*
: Record why the request is being closed, alongside the disposition

## Subcommands

**ls**
: List open requests, oldest first (the default)

**dispose**
: Close one open request by id

## Examples

*List open requests*

```sh
magus attention
```

*Full records as JSON*

```sh
magus attention ls -o json
```

*Close one request*

```sh
magus attention dispose att-3f9c1a2b4d5e
```

*Close one, saying why*

```sh
magus attention dispose att-3f9c1a2b4d5e -reason "approved and pushed by hand"
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-sessions**(1)](magus-sessions.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

