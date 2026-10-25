---
title: magus gate
generated_from: internal/commands/registry.go
description: "Record a person's waiver of the push gate for one commit, or revoke it."
tags: [cli, magus gate, gate, push, waiver]
---

# magus-gate

A person's decisions about the local gate

## Synopsis

**magus** gate \<waive\> [flags]

### gate waive options

**--reason** *string*
: Why the gate was skipped, recorded with the waiver; required unless --revoke

**--revoke**
: Remove the commit's waiver

## Subcommands

**waive**
: Let one commit be pushed with no passing gate, typed by a person

## Examples

*Push HEAD without the local gate, saying why*

```sh
magus gate waive HEAD --reason "CI gates this release branch"
```

*Take the waiver back*

```sh
magus gate waive --revoke HEAD
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-where**(1)](magus-where.md), [**magus-run**(1)](magus-run.md), [**magus-affected**(1)](magus-affected.md), [**magus-x**(1)](magus-x.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-query**(1)](magus-query.md), [**magus-refs**(1)](magus-refs.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-graph**(1)](magus-graph.md), [**magus-diff**(1)](magus-diff.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-session**(1)](magus-session.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-queue**(1)](magus-queue.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-status**(1)](magus-status.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-init**(1)](magus-init.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

