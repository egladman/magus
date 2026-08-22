---
title: magus hook
generated_from: internal/clispec/registry.go
description: "Read one shell command, or one path an edit is about to write, and report a deny, advise, or pass verdict for an agent host's pre-tool-use hook."
tags: [cli, magus hook, guard, agents, policy, pre-tool-use]
---

# magus-hook

Evaluate one shell command or file path against the magus guard rules

## Synopsis

**magus** hook [--path] [flags]

## Description

Evaluate ONE shell command, or one file path an edit is about to write,
against this workspace's guard rules, and report a deny, advise, or pass
verdict.

It is built for an agent host's pre-tool-use hook. The input arrives on
stdin, so nothing has to survive being quoted through a shell twice.

Two input shapes are accepted. Plain text is the command (or the path)
itself. A JSON envelope from a host that writes one needs neither --path nor
a JSON tool to unwrap it: the envelope already says what is about to run and
whether it is a write. An explicit flag still wins, because a wrapper that
passed one meant it.

--observe records a path the agent merely REACHED, without judging it. No rule
applies to a read, so the verdict is always pass and the activity event
previews as observed rather than as a guard decision. Which of a host's tools
only look is the wrapper's knowledge, never magus's.

--agent-name, --session, --transcript, and --event are attribution, not policy.
They record who produced the observation on the activity event, and the verdict
never reads them. All are optional and unvalidated, including the host name,
which is an opaque label the caller chooses rather than a set magus knows: a
magus that enumerated hosts would need a release per host, and a wrapper that
cannot extract a session id must still be able to get a verdict.

## Options

**--agent-name** *string*
: Name of the agent host this invocation came from (attribution only)

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

## Examples

*Judge a shell command*

```sh
printf '%s' 'go build ./...' | magus hook
```

*Judge a path an edit is about to write*

```sh
printf '%s' 'MAGUS.md' | magus hook --path
```

*Record a path an agent read, without judging it*

```sh
printf '%s' 'internal/cache/output.go' | magus hook --observe
```

*Machine-readable verdict*

```sh
printf '%s' 'rm -rf /' | magus hook -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-activity**(1)](magus-activity.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

