---
title: magus mcp
generated_from: internal/cli/registry.go
description: Print the MCP endpoint, its auth token command, and how to point a client at it. MCP is served by the daemon (magus server start), not run as its own process.
tags: [cli, magus mcp, mcp, agent, daemon]
---

# magus-mcp

Print how to reach the MCP server

## Synopsis

**magus** mcp

## Description

MCP is not a standalone process: it is served by magus server, alongside
everything else it hosts. This command prints what a client
needs to reach it - the endpoint, the auth token command, and a liveness
probe - and exits non-zero, since it starts nothing itself.

magus server start                    start the server (MCP comes up with it)
  magus config mcp connector create     mint a bearer token for one client
  magus status --probe=liveness,mcp     confirm the endpoint is serving

Per-client configuration lives in docs/guides/integrations/mcp.md, not in
this binary: naming a client here would make a change to its config format a
magus release.

## Exit status

**2**
: Always: mcp prints reach-it instructions and starts nothing, so the invocation is treated like any other command that named a retired verb.

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-queue**(1)](magus-queue.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

