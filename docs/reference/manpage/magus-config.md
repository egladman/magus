---
title: magus config
description: Inspect the effective merged configuration or write keys to the local or global magus.yaml, with subcommands for view, set, history, cache, and mcp.
tags: [cli, magus config, configuration, magus.yaml, settings, cache]
---

# magus-config

View or update magus configuration

## Synopsis

**magus** config \<view|set|history|cache|mcp\> [flags]

## Description

Inspect or modify the magus configuration. Configuration is loaded in
priority order: built-in defaults → user-global file → workspace file →
project-local file → MAGUS_\* environment variables → CLI flags.

The view sub-command prints the effective merged configuration. The set
sub-command writes a key-value pair to the local (or global) config file.
The init sub-command materializes the built-in defaults to a magus.yaml so
they can be edited by hand.

Configuration is stored in magus.yaml (or .magus.yaml). The canonical
locations are the workspace root and $XDG_CONFIG_HOME/magus/.

## Subcommands

**view**
: Print the effective configuration (defaults + file + env)

**set**
: Write a key to the local (or global) config file

**history**
: Manage forecaster runtime history

**cache**
: Manage the build cache (prune)

**mcp**
: Manage the MCP server auth token

## Examples

*Show effective config*

```sh
magus config view
```

*Show config as JSON*

```sh
magus config view -o json
```

*Set cache to read-only*

```sh
magus config set key=cache.immutable,value=true
```

*Prune local cache entries older than a week*

```sh
magus config cache prune --older-than 168h
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-insight**(1)](magus-insight.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-memory**(1)](magus-memory.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

