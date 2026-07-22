---
title: "MGS2003: env vars stripped"
description: Informational notice that the sandbox replaced a child process environment with a curated allowlist, dropping variables like tokens or credentials.
tags:
  [MGS2003, sandbox, security, environment, passthrough, secrets, subprocess]
---

# MGS2003: env vars stripped from child process

The sandbox replaced a child process's inherited environment with a
curated allowlist and one or more variables were dropped.

```text
[MGS2003] env vars stripped from child process by sandbox
  cmd=go stripped_count=14
```

## Why

The default allowlist is intentionally small: `HOME`, `PATH`, `USER`,
locale and terminal vars, plus the `MAGUS_*` daemon coordination vars.
Everything else is stripped so that a compromised spell cannot
exfiltrate `AWS_ACCESS_KEY_ID`, `GITHUB_TOKEN`, `VAULT_TOKEN`,
`OP_SESSION_*`, `NPM_TOKEN`, `ANTHROPIC_API_KEY`, and similar.

The warning is informational, not an error. The build may have
succeeded; this message exists so you can spot when a tool's behaviour
changes because a variable it expected is no longer present.

## Resolution

If a tool depends on a non-secret variable being inherited (a Go build
that needs `GOPATH`, an npm build that needs `NPM_CONFIG_CACHE`, mise
shims that need `MISE_*`), add it to the passthrough list:

```yaml
sandbox:
  env:
    passthrough:
      - GOPATH
      - GOCACHE
      - "MISE_*"
```

The `*` is a suffix wildcard: `MISE_*` matches every name starting with
`MISE_`. It is intentionally not full glob syntax.

If a tool depends on a secret-bearing variable (`GITHUB_TOKEN`, etc.),
think carefully before granting it. The sandbox exists to keep those
out of subprocesses. If the tool genuinely needs the token, consider
passing it via a file path the spell reads explicitly rather than
re-broadcasting it through the environment.
