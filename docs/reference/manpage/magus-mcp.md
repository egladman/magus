---
title: magus mcp
generated_from: internal/cli/registry.go
description: Serve the MCP tools over stdin and stdout for the agent host that launched the process, against the workspace it was launched in, with no daemon and no token.
tags: [cli, magus mcp, mcp, agent, stdio]
---

# magus-mcp

Serve MCP over stdio for the agent host that launched it

## Synopsis

**magus** mcp

## Description

Serve MCP over stdin and stdout, one JSON-RPC message per line, for the
agent host that launched this process. It opens the workspace it is launched
in and needs no daemon and no bearer token: the caller is the local process
the host started, admitted with mcp=write, and every tool call is recorded on
the activity trail with the stdio credential and the client's name.

Stdout is the protocol wire and carries nothing else; logs and one line
saying what is being served go to stderr. It stops when the host closes
stdin, or on Ctrl+C.

Register it with an MCP client as a stdio server:

command  magus
  args     ["mcp"]

The daemon (magus server start) serves the same tools over Streamable HTTP
for one long-lived server shared by several clients; that endpoint takes a
connector token (magus config mcp connector create).

Per-client configuration lives in docs/guides/integrations/mcp.md, not in
this binary: naming a client here would make a change to its config format a
magus release.

## Exit status

**0**
: The host closed stdin.

**1**
: The workspace did not load, or reading stdin or writing stdout failed.

**2**
: An argument was given; mcp takes none.

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-queue**(1)](magus-queue.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

