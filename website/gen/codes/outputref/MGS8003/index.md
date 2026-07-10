---
title: "MGS8003: malformed output ref"
description: Fires when magus query output is given an argument that is not a valid ref<hex> id, so the value cannot name a stored output.
tags: [MGS8003, output refs, query, flags]
---

# MGS8003: malformed output ref

`magus query output <ref>` retrieves one target execution's captured output by its
reference id (`magus query output ref1a2b3c`). This code fires when the argument is
not a well-formed `ref<hex>` id, so it cannot name a stored output.

`output` is an explicit subcommand, so a bare search term is never mistaken for a
ref: `magus query refactor` queries the knowledge graph. This code is reserved for
the case where you asked for `output` retrieval but the id is malformed.

## Resolution

Pass a valid reference id (printed on each target's result line when it runs), or
drop `output` to run a knowledge-graph search. To open the knowledge graph in a
browser instead, use `magus graph open`.
