---
title: Diagnostics and wards
page_type: overview
description: A curated map of every coded diagnostic family (MGSxxxx) by range, from magusfile authoring mistakes and sandbox denials to daemon auth and knowledge-graph extraction ambiguities.
tags: [diagnostics, codes, MGS, error codes, wards, reference]
---

# Diagnostics and wards

Every magus error is a pointable coded diagnostic (MGSxxxx); see
[Diagnostics](diagnostics.md) for how the codes are raised and resolved.
This page lists the families by range. See also
[Wards](../concepts/wards.md), the guardrail mechanism that rejects a
resolved op whose argv contradicts its declared kind - most of the coded
families below are wards firing.

- [MGS1xxx - magusfile diagnostics](codes/magusfile/README.md) - authoring mistakes in a workspace's magusfile, such as missing targets or unresolved declarations.
- [MGS2xxx - sandbox diagnostics](codes/sandbox/README.md) - denied reads, writes, execs, and env leaks from the sandbox that confines spells to the workspace.
- [MGS4xxx - race diagnostics](codes/race/README.md) - race conditions emitted by the magus race detector across static, watch, and replay modes.
- [MGS5xxx - services diagnostics](codes/services/README.md) - long-running service ops that should be shared instead of run as separate processes.
- [MGS6xxx - charm diagnostics](codes/charms/README.md) - a charm's JSON Patch that is valid in shape but does not apply to a target's command.
- [MGS7xxx - knowledge-graph diagnostics](codes/knowledge/README.md) - ambiguities the graph extractor hits while assembling the deterministic graph, such as an unresolved buzz import.
- [MGS8xxx - output-reference diagnostics](codes/outputref/README.md) - problems resolving a target-output reference id with `magus query output <ref>`.
- [MGS9xxx - auth diagnostics](codes/auth/README.md) - the daemon's bearer-token auth: the operator token and connector tokens minted for MCP clients.
