---
title: Integrations
page_type: overview
description: A curated map of the integrations section - wiring magus into the coding agents, editors, CI providers, and IDE plugins that already sit in your loop.
tags: [integrations, overview, index, agents, mcp, ci, daemon, editor]
---

# Integrations

magus is not meant to run in isolation. These pages cover wiring it into the
other tools already in your loop: coding agents, editors, CI, and anything
that talks to the daemon instead of a terminal.

- [Agents](integrations/agents.md) - the installable skills, guard hook, and MCP setup for coding agents.
- [MCP](integrations/mcp.md) - drive magus from agents and IDE plugins over the Model Context Protocol.
- [CI checkout](integrations/ci.md) - why a blobless partial clone is the right default, and how magus deepens a shallow clone itself.
- [Daemon and concurrency](integrations/daemon.md) - one persistent process, one shared pool across every client.
- [Editor setup](integrations/editor.md) - wire `magus buzz lsp` for completion, hover, and signature help.
