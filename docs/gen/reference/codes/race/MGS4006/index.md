---
title: "MGS4006: stale generated output"
description: A generated output drifted on regeneration because a declared input actually changed, so the committed output is stale and must be regenerated and committed.
tags:
  [MGS4006, drift, generated, stale, inputs, regenerate]
---

# MGS4006: stale generated output

A generated output changed when you regenerated it, and one of its declared inputs is
also dirty. The output is stale: a source input moved on from the version the committed
output was built from, so the committed output no longer reflects its inputs.

```text
[MGS4006] generated output drifted and a declared input changed; re-run
`magus run generate:rw` and commit
```

## Why

This is real content drift, not toolchain noise (contrast [MGS4005](MGS4005.md), where
the inputs are unchanged). A declared input - a source file the generator reads - changed
without the generated output being regenerated to match. Left uncommitted, the repository
ships a generated file that disagrees with its own source.

## Resolution

Regenerate and commit: `magus run generate:rw`, then commit the updated outputs alongside
the input change that caused them. This is the expected, healthy path - the output simply
needs to catch up with its inputs.
