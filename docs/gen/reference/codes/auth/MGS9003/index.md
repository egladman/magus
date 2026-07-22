---
title: "MGS9003: connector store is too new"
description: The connector token store on disk was written by a newer magus than the one reading it, so its format may not be understood. Upgrade magus.
tags: [MGS9003, auth, connector, store, version, upgrade]
---

# MGS9003: connector store is too new

The connector token store file records a schema version higher than the running
magus understands. A newer magus wrote it; this older one refuses to read it
rather than risk misparsing credentials.

```text
[MGS9003] auth: connector store /path is version 3, newer than this magus
supports (2); upgrade magus
  see: .../MGS9003.md
```

## Why

The store is versioned so its format can evolve. Reading a newer version with an
older magus could drop fields or corrupt the file on the next write. Magus fails
closed instead.

## Resolution

Upgrade magus to a version at least as new as the one that wrote the store
(usually the newest release). If you intentionally downgraded, either upgrade
back, or move the newer store aside and re-mint connector tokens with the older
version.

## See also

- `magus version`: the running magus's version.
- `magus config mcp connector`: manage connector tokens.
