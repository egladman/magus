---
title: magus session
generated_from: internal/clispec/registry.go
description: "One family over the repository's session store: list what sessions ran, list the blocks agents raised, close one by hand, and take the host-hook ingest that writes it all."
tags: [cli, magus session, sessions, attention, history, worktrees, agents, guard]
---

# magus-session

What sessions did and what they are blocked on: humans read and dispose, hosts write

## Synopsis

**magus** session [ls] [flags]

## Description

One noun over the repository's session store, with a human side and a
machine side.

Humans read it. The bare command lists past magus sessions with the targets
each one ran and how those runs ended; \`session attention\` lists the requests
agents have raised - work blocked on input or on approval - and
\`session dispose\` closes one. Nothing closes a request automatically: there is
no expiry, no severity inference and no auto-dispose flag, because a request
magus could answer by itself would not have needed a person - see the doctrine
page.

Agent hosts write it. Their hooks pipe every event through \`session hook\`
(one command or path, judged against the guard rules and recorded) and
\`session notify\` (one attention event, normalized; a waiting or permission
outcome opens a durable request). No person types those two; they exist to be
wired.

The store is keyed by repository identity rather than by checkout path, so
every git worktree of one repo reads and writes the same records - what
another worktree just finished, and what it is blocked on, is visible here
without a daemon, a network, or a shared branch. It is append-only and never
rewritten; a line left half-written by a killed process is skipped and counted
rather than failing the read.

The listing takes --limit to bound by count and --since to bound by AGE, as a
duration back from now (2h, 45m, 168h) or an RFC3339 instant. --since compares
against each session's last fact, not its first, so a long session that is
still working stays listed however long ago it began.

## Options

**--limit** *int*
: Show at most this many sessions (0 for all)

**--since** *string*
: Show only sessions active since this point: a duration back from now (2h, 45m, 168h) or an RFC3339 timestamp

### session dispose options

**--reason** *string*
: Record why the request is being closed, alongside the disposition

### session hook options

**--agent-name** *string*
: Name of the agent host this invocation came from (attribution only)

**--delegation** *string*
: The delegation this call is acting as, graded against the ledger's declared write boundary (defaults to $MAGUS_DELEGATION)

**--event** *string*
: The host's hook event name (e.g. PreToolUse)

**--observe**
: Record the input as a path the agent reached, without judging it: no rule applies and the verdict is always pass

**--path**
: Judge the input as a file path an edit is about to write, not as a shell command

**--session** *string*
: The host's own session id for this invocation

**--transcript** *string*
: Path to the host's own log of this session, recorded as a pointer; magus never opens it

### session notify options

**--desktop**
: Also raise an OS notification

**--outcome** *string*
: Outcome vocabulary for the event

## Subcommands

**ls**
: List past sessions and the targets they ran (the default)

**attention**
: List the open requests agents raised, oldest first; with -q, print nothing and exit 1 when the queue is empty

**dispose**
: Close one open request by id or unambiguous id prefix

**hook**
: Evaluate one shell command or file path against the magus guard rules

**notify**
: Normalize an attention event and optionally notify the local desktop

## Exit status

**0**
: Sessions or requests were listed, a request was disposed, an event was normalized and emitted, or hook judged the input allowed (pass, or advise, which attaches context and does not block; --observe always lands here). A plain listing exits 0 whether or not anything was listed, because an empty queue is the good state. notify's delivery is best-effort and never changes this: a desktop notification that could not be raised, and a durable request that could not be opened, are both reported as warnings and still exit 0.

**1**
: dispose: the request named is not in the store, or was already disposed - a request closes once and stays closed. attention with -q: the queue is empty, so a prompt or a watch loop can branch on the status instead of parsing the listing. notify: stdin could not be read (unparsable input is not this case - text that is not a complete event envelope becomes the event's message rather than an error).

**2**
: Misuse: an unknown subcommand, an argument to a listing, or a dispose naming other than exactly one id. For hook, also a DENIED command - deny and malformed input share the code deliberately: a guard that could not parse its input has not cleared the command either, so a host that blocks on 2 fails closed in both cases.

## Examples

*Show recent sessions*

```sh
magus session
```

*Show today's work*

```sh
magus session --since 24h
```

*Full session records as JSON*

```sh
magus session -o json
```

*List open attention requests*

```sh
magus session attention
```

*Ask whether anyone is waiting*

```sh
magus session attention -q
```

*Close one request, saying why*

```sh
magus session dispose att-3f9c -reason "approved and pushed by hand"
```

*Judge a shell command (host-wired)*

```sh
printf '%s' 'go build ./...' | magus session hook
```

*Record a path an agent read, without judging it*

```sh
printf '%s' 'internal/cache/output.go' | magus session hook --observe
```

*Grade a write as a delegation*

```sh
printf '%s' 'internal/ledger/store.go' | magus session hook --path --delegation f2-guard
```

*Raise a permission prompt on the desktop (host-wired)*

```sh
printf '%s\n' 'needs approval' | magus session notify --outcome permission --desktop
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

