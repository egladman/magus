---
title: Guides
page_type: overview
description: A curated map of the guides section - getting magus running, the day-to-day commands, and wiring it into agents, editors, and the daemon.
tags: [guides, overview, index, reading-order]
---

# Guides

Task-oriented walkthroughs, as opposed to the [concepts](concepts.md) pages
that explain the model. Pick a path: fast if you already know what you want,
linear if you would rather have it explained as you go.

## Get running

- [Download](guides/download.md) - install a signed release, verify it, and set up your shell.
- [Getting started](guides/getting-started.md) - the linear walkthrough from install to your first `ci` pipeline.
- [Quick start](guides/quickstart.md) - the same ground on one dense page, for when you want to move now and read deeper later.

## Day to day

- [Debugging](guides/debugging.md) - the interactive REPL and `magus\pry()` breakpoints.
- [Testing](guides/testing.md) - the `test` block, and the `assert`/`suite` libraries.
- [Tips and tricks](guides/tips.md) - non-obvious ways to combine subcommands.

## Integrations

Wiring magus into the rest of your loop. The [Integrations overview](guides/integrations.md) covers these in one place, including CI checkout.

- [Agents](guides/integrations/agents.md) - the installable skills, guard hook, and MCP setup for coding agents.
- [MCP](guides/integrations/mcp.md) - drive magus from agents and IDE plugins over the Model Context Protocol.
- [Daemon and concurrency](guides/integrations/daemon.md) - one persistent process, one shared pool across every client.
- [Editor setup](guides/integrations/editor.md) - wire `magus buzz lsp` for completion, hover, and signature help.
