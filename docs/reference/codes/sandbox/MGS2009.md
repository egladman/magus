---
title: "MGS2009: outbound network request"
description: Fires at info level for every outbound network request a spell makes through the http bindings while the sandbox is active. The sandbox audits egress but does not block it.
tags: [MGS2009, sandbox, network, egress, http, audit, ssrf]
---

# MGS2009: outbound network request (audited, not blocked)

A spell made an outbound network request through the `http.*` bindings while
the sandbox was active. The sandbox logs every such request for audit, but
does not block it.

```text
[MGS2009] sandbox: outbound network request
  method=GET url=https://api.example.com/v1/data
```

## Why

Sandbox v1 confines the filesystem and the child environment, but does not
block network egress: a compromised spell with no token in its env can still
reach an arbitrary host. Closing that gap needs an opt-in network policy
(a future `sandbox_network` flag), which does not exist yet.

Until then the sandbox does the next best thing and keeps an audit trail.
Every request issued through the built-in `http.*` bindings is recorded at
info level with its method and URL before it is sent. There is no SSRF
allow/deny enforcement: the request is still fetched, including localhost,
RFC1918 ranges, and the cloud metadata endpoint (169.254.169.254). Treat any
URL reachable from a magusfile as trusted.

## Resolution

This is informational, not an error. In normal use it documents the network
calls your spells make.

1. **If the host is one you expect** (the spell's documented API, a package
   registry, etc.): nothing to do.

2. **If you see a request to a host you do not recognize:** treat it as a
   red flag and investigate the spell that issued it. The URL in the log is
   the exact destination. There is no built-in way to block egress yet, so
   the safe response is to stop running the untrusted spell.
