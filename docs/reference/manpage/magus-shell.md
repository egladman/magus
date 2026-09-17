---
title: magus shell
generated_from: internal/cli/registry.go
description: Read one shell command, or one path an edit is about to write, and report what this workspace would rather you ran.
tags: [cli, magus shell, guard, conventions, hook, pre-tool-use]
---

# magus-shell

Check a command against this workspace's conventions before running it

## Synopsis

**magus** shell '\<command\>' [flags]

## Description

Check a shell command against this workspace's conventions, and name the
better command when there is one.

The rules are the workspace's own. A raw \`go build\` misses the cache and the
affected set; a recursive grep misses what the symbol index already knows;
\`git add -A\` sweeps regenerated output into a commit about something else.
None of that is a fact about who typed the command, which is why this is a
plain subcommand rather than something under an agent namespace.

Nothing is executed and nothing is prevented. This reports; you decide.

THE INPUT ARRIVES TWO WAYS, and they are the same command either way. A person
passes it as one quoted argument. A host pipes it on stdin, where it may be
plain text or the JSON envelope the host already writes, so nothing has to
survive being quoted through a shell twice. That is deliberate: an agent and a
person get the same verdict from the same entry point, and neither reads
documentation the other cannot.

--path judges the input as a file path an edit is about to write, rather than
as a shell command.

--observe records a path the agent merely REACHED, without judging it. No rule
applies to a read, so the verdict is always pass and the activity event
previews as observed rather than as a guard decision. Which of a host's tools
only look is the caller's knowledge, never magus's.

--agent-name, --session, --transcript, and --event are attribution, not policy.
They record who produced the observation on the activity event, and the verdict
never reads them. All are optional and unvalidated, including the host name,
which is an opaque label the caller chooses rather than a set magus knows: a
magus that enumerated hosts would need a release per host, and a caller that
cannot extract a session id must still be able to get a verdict.

--lease is the exception: it IS policy. It names the lease the caller is acting
as, and a write is then graded against that lease's declared write boundary in
this workspace's lease ledger. Inside its write paths passes; inside its deny
paths, or inside another live lease's write paths, is denied and the reason
names the owning lease. It defaults to the magus.lease member of $BAGGAGE - the
W3C baggage list a spawning tool exports - and the flag wins when both are set.

A call that names no valid lease while a fleet is running is ADVISED and never
blocked: a person editing their own repository has no lease id, and the guard is
a seatbelt for callers that opt in rather than a sandbox. With no ledger, or
with no lease in it declared or running, nothing is graded and nothing is read.

EXIT CODES are the contract a host blocks on: 2 is a deny and everything else is
allowed. An advisory exits 0 on purpose - it attaches context and does not block,
and a suggestion that failed a script would not be a suggestion. Input that could
not be READ is 2, so a host that blocks on 2 fails closed when bytes were lost on
the way in; an EMPTY input passes, because a wrapper that hands this nothing must
not block every tool call.

## Options

**--agent-name** *string*
: Name of the agent host this invocation came from (attribution only)

**--event** *string*
: The host's hook event name (e.g. PreToolUse)

**--lease** *string*
: The lease this call is acting as, graded against the ledger's declared write boundary (defaults to magus.lease in $BAGGAGE)

**--observe**
: Record the input as a path the agent reached, without judging it: no rule applies and the verdict is always pass

**--observes-skill-loads**
: This host's wiring reports skill loads to magus, so a rule may require one before a spawn; without it those rules stand down

**--path**
: Judge the input as a file path an edit is about to write, not as a shell command

**--session** *string*
: The host's own session id for this invocation

**--transcript** *string*
: Path to the host's own log of this session, recorded as a pointer; magus never opens it

## Exit status

**0**
: The input is allowed: pass, or advise, which attaches context and does not block. --observe always lands here, because it judges nothing. An EMPTY input is also 0: a wrapper that hands this nothing must not block every tool call.

**1**
: Not returned by a verdict. A wrapper should treat anything other than 2 as allowed rather than enumerating codes, so that a future signal added here does not start blocking commands.

**2**
: A DENIED command or path, and also input that could not be READ - the two share the code deliberately: a guard that could not parse its input has not cleared the command either, so a host that blocks on 2 fails closed in both cases. Misuse (an unquoted command, an unknown flag) is also 2.

## Examples

*Check one command*

```sh
magus shell 'go test ./...'
```

*Check a search*

```sh
magus shell 'grep -rn HandleFoo internal/'
```

*Judge a path an edit would write*

```sh
magus shell --path MAGUS.md
```

*As a host's pre-tool-use hook*

```sh
printf '%s' 'go build ./...' | magus shell
```

*Record a path an agent read, without judging it*

```sh
printf '%s' 'internal/cache/output.go' | magus shell --observe
```

*Grade a write as a lease*

```sh
printf '%s' 'internal/job/store.go' | magus shell --path --lease f2-guard
```

*The verdict as JSON*

```sh
magus shell 'git add -A' -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

