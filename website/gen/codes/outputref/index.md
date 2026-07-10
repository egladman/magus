---
title: output-reference diagnostics
page_type: overview
description: Landing page for MGS8xxx diagnostics that flag problems resolving a target-output reference id with magus query, such as a ref that aged out of the cache, an ambiguous prefix, or a malformed ref passed to a ref-only flag.
tags:
  [output refs, diagnostics, error codes, MGS8xxx, query, cache]
---

# Output-reference diagnostics

Codes in the `MGS8xxx` range flag problems resolving a target-output reference
id (`ref1a2b3c`) with `magus query output <ref>`. Every target that runs is given
a short reference id for its captured output; these codes fire when that id cannot
be resolved to stored output.

## Codes

- [MGS8001](MGS8001.md): the ref is well-formed but no stored output exists for
  it - it aged out of the cache, or the ref is mistyped.
- [MGS8002](MGS8002.md): a shortened ref prefix matches more than one stored
  output, so the lookup is ambiguous.
- [MGS8003](MGS8003.md): `magus query output` was given an argument that is not a
  well-formed `ref<hex>` id, so it cannot name a stored output.
