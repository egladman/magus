---
title: magus ledger
generated_from: internal/cli/registry.go
description: "Read the per-repository lease ledger: the leases an orchestrating agent declared, as a tree, plus the worker brief for any one of them."
tags: [cli, magus ledger, ledger, leases, agents, delegation]
---

# magus-ledger

Read the lease ledger a fan-out declared

## Synopsis

**magus** ledger [ls|brief \<lease-id\>] [flags]

## Description

Read the lease ledger: one row per lease an orchestrating agent declared,
rendered as a tree of parents and the leases they handed out.

The ledger is written by AGENTS, through the magus_ledger MCP tool, and read by
PEOPLE here. put, register and clear are deliberately not on this verb: the plan
has one author by definition of what it records, and a second write door invites
two.

The rows are kept per repository rather than per checkout, so an orchestrator
declaring a plan in one worktree and a worker reading it in another see the same
book.

brief renders one lease's worker brief: the row's own goal and acceptance
criteria, its owned and forbidden paths, the knowledge graph's blast radius for
each owned path it can resolve, the single validation target that lease is
allowed to run, its dependencies, and the fixed bootstrap, rules and skills
blocks this workspace's docs/guides/integrations/agents/brief.md.tmpl carries. It
is context and never a verdict, the same shape magus diff --prompt has: magus
assembles what it holds, and you hand it to the worker.

## Subcommands

**ls**
: Print the declared leases as a tree, with the overlapping pairs

**brief**
: Print one lease's worker brief

## Examples

*Read the declared plan*

```sh
magus ledger
```

*Read it as records*

```sh
magus ledger -o json
```

*Brief one worker*

```sh
magus ledger brief session-load/core
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

