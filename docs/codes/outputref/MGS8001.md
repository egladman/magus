---
title: "MGS8001: output ref not found"
description: Fires when magus query output <ref> names a well-formed reference id but no captured output is stored for it, because it aged out of the cache or the ref is mistyped.
tags: [MGS8001, output refs, query, cache, retrieval]
---

# MGS8001: output ref not found

Every target that runs is given a short reference id (`ref1a2b3c`) for its
captured output, retrievable later with `magus query output <ref>`. This code fires
when the ref is well-formed but the store holds no output for it.

There are two ordinary causes: the entry aged out of the cache (the store keeps
only the last few executions per target and prunes by age and size), or the ref
was mistyped or copied incompletely.

## Resolution

Re-run the target to regenerate its output and mint a fresh ref, then use that
ref. If you expected the ref to still be present, confirm you copied it whole (a
truncated prefix that matches nothing also lands here) and that the cache has
not since been cleared.
