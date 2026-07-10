---
title: "MGS8003: malformed output ref"
description: Fires when a ref-only flag (--open, --meta, --print) is given without a valid ref<hex> argument, so the value cannot name a stored output.
tags: [MGS8003, output refs, query, flags]
---

# MGS8003: malformed output ref

The `--open`, `--meta`, and `--print` flags on `magus query` apply ONLY to an
output reference id (`magus query ref1a2b3c --open`). This code fires when one of
those flags is set but the argument is not a valid `ref<hex>` id, so it cannot
name a stored output.

A bare, ref-shaped positional with no flags is treated as a search term instead,
so `magus query refactor` still queries the knowledge graph; this code is
reserved for the case where a ref-only flag makes the intent unambiguous.

## Resolution

Pass a valid reference id (printed on each target's result line when it runs), or
drop the flag. To open the knowledge graph in a browser instead, use
`magus graph open`.
