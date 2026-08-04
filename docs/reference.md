---
title: Reference
page_type: overview
description: A curated map of the reference section - the CLI, config, diagnostics, the magusfile API, and every daemon and embedding surface, grouped by the question you came here to answer.
tags: [reference, overview, index, cli, config, diagnostics, api]
---

# Reference

Lookup material, as opposed to the [concepts](concepts.md) pages that explain
the model or the [guides](guides.md) that walk you through a task. Grouped by
the question you came here with.

## Command line

- [The CLI in practice](reference/cli.md) - the subcommands you actually reach for, grouped by the question you are asking, with the quirks that are not obvious from the help text.
- [Man pages](reference/manpage.md) - the full section-1 man page set, one page per subcommand.

## Configuration and behavior

- [magus.yaml configuration](reference/config.md) - every config key with its MAGUS_* environment variable, CLI flag, and type.
- [Logging and verbosity](reference/logging.md) - what each verbosity level actually prints, and what ends up in your logs that you may not want to paste into a bug report.
- [Console API](reference/console.md) - the read-only loopback API that lets the Graph Explorer show your live workspace.

## The magusfile API

- [magus stdlib](reference/buzz/index.md) - every stdlib module available to a magusfile: fs, os, http, json, yaml, crypto, and the rest.
- [Agent skills](reference/skills/index.md) - every skill magus installs for coding agents, generated from the embedded bodies.

## When something goes wrong

- [Diagnostics](reference/diagnostics.md) - every magus error is a pointable coded diagnostic (MGSxxxx) with a handwritten resolution page and a queryable graph node.
- [Diagnostics and wards](reference/codes.md) - the coded diagnostic families (MGSxxxx) by range, and the wards that guard resolved ops.
- [FAQ](reference/faq.md) - short answers to the questions that come up first: spells versus targets versus charms, why runs are read-only, how the cache decides.

## Embedding magus

- [The Go SDK](reference/go-sdk.md) - use magus as a Go library instead of the CLI: Open vs Inspect, the interface hierarchy, ctx and cancellation.
- [Daemon API](reference/api/index.md) - the daemon's Connect, gRPC, and gRPC-Web API: every service, method, message, and enum, generated from the .proto contract.
