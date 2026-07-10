---
title: "MGS8002: ambiguous output ref prefix"
description: Fires when a shortened magus query <ref> prefix matches more than one stored output reference, so magus cannot tell which one you meant.
tags: [MGS8002, output refs, query, prefix, ambiguous]
---

# MGS8002: ambiguous output ref prefix

`magus query <ref>` accepts a unique prefix of a reference id, the same way git
accepts a short commit hash. This code fires when the prefix you gave matches
more than one stored ref, so the lookup is ambiguous.

## Resolution

Add more characters until the prefix is unique. The error lists the matching
refs; copy one in full. Full reference ids are printed on each target's result
line when it runs.
