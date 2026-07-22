---
title: "MGS9002: insecure token file permissions"
description: A magus auth token or connector store file is readable beyond its owner. Magus refuses to use it until the permissions are tightened to 0600.
tags: [MGS9002, auth, token, connector, permissions, security]
---

# MGS9002: insecure token file permissions

A magus secret file - the operator (cli) token, or the connector store - has
filesystem permissions looser than `0600`, so a user other than its owner could
read the secret. Magus refuses to load it rather than trust a world- or
group-readable credential.

```text
[MGS9002] auth: token file /path has insecure permissions 0644 (want 0600);
fix with: chmod 600 /path
  see: .../MGS9002.md
```

## Why

These files hold bearer secrets. A secret readable by other accounts on the
machine is effectively shared. Magus treats loose permissions as a hard error
(not a warning) so a leaked-by-default credential cannot be used silently.

## Resolution

Tighten the file to owner-only read/write, exactly as the message says:

```sh
chmod 600 <path>
```

Then re-run the command. If the file lives on a filesystem that cannot represent
Unix permissions, move the magus state directory to one that can.

## See also

- `magus config mcp token`: where the operator token lives.
- `magus config mcp connector`: the connector store.
