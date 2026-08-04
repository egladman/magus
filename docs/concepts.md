---
title: Concepts
page_type: overview
description: A curated map of the concepts section - the domain model magus is built on, grouped into a suggested reading order instead of a raw file list.
tags: [concepts, overview, index, reading-order, domain-model]
---

# Concepts

This section explains the model magus is built on: what a workspace and a
target are, how a run is scheduled and cached, and the guardrails that keep it
honest. Each page stands alone, but they build on each other - read them in
roughly this order the first time through.

## Start here

The five ideas everything else composes from.

- [Workspace and projects](concepts/workspace.md) - the discovered root, and how a directory becomes a project.
- [Targets](concepts/targets.md) - the addressable unit of work: a project path plus an operation name.
- [Dependencies](concepts/dependencies.md) - `magus\needs` (target-level) versus `depends_on` (project-level), and how the two fold together.
- [Spells](concepts/spells.md) - toolchain libraries (go, rust, ...) that contribute the ops your targets compose.
- [Charms](concepts/charms.md) - composable execution modifiers attached with `:`, like `lint:rw`.

## The machinery

How a run actually gets scheduled, cached, and confined.

- [Operations](concepts/operations.md) - where an op sits in the spell -> target -> process hierarchy.
- [Cache model](concepts/cache.md) - the content-addressed cache key, and replaying a hit without rerunning the body.
- [Concurrency](concepts/concurrency.md) - the in-process scheduler, and the cross-process workspace lock.
- [Sandbox model](concepts/sandbox.md) - the threat model and allowlist semantics around spell execution.
- [Services](concepts/services.md) - long-running service ops shared across dependents and invocations.
- [Secrets](concepts/secrets.md) - resolving a credential through a provider so magus knows to redact it.
- [Engines](concepts/engines.md) - the seam that runs a magusfile on the embedded Buzz VM.

## Guardrails and observability

What keeps a run honest, and what tells you what happened.

- [Wards](concepts/wards.md) - coded guardrails that reject a resolved op whose argv contradicts its kind.
- [Volatility](concepts/volatility.md) - telling a flaky failure from a real regression.
- [Telemetry](concepts/telemetry.md) - OTLP traces and metrics for a run.
- [CI providers](concepts/ci-providers.md) - teach magus your CI system's log-grouping and annotation syntax.
- [Knowledge graph](concepts/knowledge.md) - the deterministic graph of the magus domain that `query`/`explain`/`path` read instead of grepping.
- [Insight](concepts/knowledge/insight.md) - VCS history read through the graph: hotspots, coupling, and ownership.
