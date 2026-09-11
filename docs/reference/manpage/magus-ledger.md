---
title: magus ledger
generated_from: internal/cli/registry.go
description: "Read the per-repository lease ledger: the leases an orchestrating agent declared, as a tree, plus the worker brief for any one of them and the acceptance check for a worker's report."
tags: [cli, magus ledger, ledger, leases, agents, delegation]
---

# magus-ledger

Read the lease ledger a fan-out declared, and grade what comes back

## Synopsis

**magus** ledger [ls|brief \<lease-id\>|accept \<lease-id\>] [flags]

## Description

Read the lease ledger: one row per lease an orchestrating agent declared,
rendered as a tree of parents and the leases they handed out.

The plan is written by AGENTS, through the magus_ledger MCP tool, and read by
PEOPLE here. put, register and clear are deliberately not on this verb: the plan
has one author by definition of what it records, and a second write door invites
two. accept is the exception, and it is not a declaration: it is the verdict on a
finished worker, and it needs an exit status a tool call cannot hand a shell.

The rows are kept per repository rather than per checkout, so an orchestrator
declaring a plan in one worktree and a worker reading it in another see the same
book.

brief renders one lease's worker brief: the row's own goal and acceptance
criteria, its owned and forbidden paths, the knowledge graph's blast radius for
each owned path it can resolve, the single validation target that lease is
allowed to run, its dependencies, and the fixed bootstrap, rules and skills
blocks this workspace's docs/guides/integrations/agents/brief.md.tmpl carries. It
also carries what the WORKSPACE knows and the row's author may not have written
down: the projects the owned paths reach, the declared output globs that land
inside them, the paths a sibling lease is holding, the build inputs and workspace
config that have one owner, and the projects that change alongside the leased
ones without declaring a dependency. It is context and never a verdict, the same
shape magus diff --prompt has, with one refusal: a row whose validation is the ci
gate, or a target that chains to it, is not briefed at all. The gate runs once,
in the orchestrator's tree, after every unit lands.

accept closes the loop. It reads a worker's report as JSON on stdin and checks
what is mechanical: every changed path inside the declared owned paths, the
validation passed, and its output ref still resolving in this workspace's store.
A row that passes is recorded pass; a rejection names every rule that failed and
exits non-zero. Whether the work is GOOD stays the orchestrator's reading.

### ledger accept options

**--schema**
: Print the JSON schema a report must satisfy, and exit

## Subcommands

**ls**
: Print the declared leases as a tree, with the overlapping pairs

**brief**
: Print one lease's worker brief

**accept**
: Grade a worker's report, read as JSON on stdin, against its row

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

*Grade what it returned*

```sh
magus ledger accept session-load/core < report.json
```

*Print the report schema*

```sh
magus ledger accept --schema
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

