---
title: Tokens and grants
description: How the magus daemon decides who may use which route. Three token classes told apart by prefix, a grant of none, read or write per surface, a need declared by every route, one rule for minting, and what the activity trail records about each.
tags:
  [
    tokens,
    grants,
    auth,
    bearer,
    operator,
    connector,
    console,
    share,
    mcp,
    security,
  ]
---

# Tokens and grants

Every daemon route except the health probes and the console's app shell needs a
bearer token. This page is the model behind that: what a token may do, how a
route says what it needs, and why no token can mint a wider one.

## Grants

A **grant** is one level per surface. Levels are ordered: `none < read < write`.

| Surface   | Levels             | Reaches                                   |
| --------- | ------------------ | ----------------------------------------- |
| `tokens`  | none, write        | token management (the TokenService)       |
| `mcp`     | none, write        | the `/mcp` endpoint                       |
| `console` | none, read, write  | the console's read routes, and its writes |

`tokens=read` and `mcp=read` mean nothing, so a grant naming either is refused.
A grant renders as `mcp=write` or `console=read`; the one below holds everything.

| Preset    | Grant                                       | Held by                         |
| --------- | ------------------------------------------- | ------------------------------- |
| operator  | `tokens=write,mcp=write,console=write`      | the operator token              |
| connector | `mcp=write`                                 | an MCP client                   |
| console   | `console=write`                             | a browser tab, a console link   |
| viewer    | `console=read`                              | a second screen that only looks |
| share     | `console=read`                              | a share link, on its own listener |

## Needs

Every route declares the level it needs on one surface, and the daemon's bearer
guard compares that with the presented token's grant. It is the only place magus
decides whether a token may use a route.

| Route                                                              | Needs           |
| ------------------------------------------------------------------ | --------------- |
| `/mcp`                                                             | `mcp=write`     |
| TokenService                                                       | `tokens=write`  |
| `/api/` (the diff review, attention, plan, graph)                  | `console=write` |
| JobService, MemoryService, GraphService, `POST /api/v1/share`      | `console=write` |
| ActivityService, StatusService, InsightService, ViewerService      | `console=read`  |
| ToolService, NotesService, MetricsService, `/api/v1/events`, `/api/v1/insight` | `console=read` |

A request with no token gets `401` [MGS9011](../reference/codes/auth/MGS9011.md).
A token that is wrong, expired, revoked, or of a class this listener does not
accept gets `401` [MGS9001](../reference/codes/auth/MGS9001.md), which never says
which. A valid token whose grant is below the route's need gets `403`
[MGS9015](../reference/codes/auth/MGS9015.md), naming the need.

## Classes

A token's class is its prefix, so the daemon knows which store can hold it
before it hashes anything:

| Prefix | Class    | Lives                                  | Expires                 |
| ------ | -------- | -------------------------------------- | ----------------------- |
| `mgo_` | operator | `$XDG_STATE_HOME/magus/mcp_token`, 0600 | never; rotate it with `magus config token generate --force` |
| `mgs_` | token    | `$XDG_STATE_HOME/magus/tokens.d/<name>.json`, only its SHA-256 | 90 days by default, at most 366 |
| `mgl_` | share    | the daemon's memory                    | 15 minutes by default, at most 24 hours |

Every class has one layout: the prefix, 43 base62 characters of randomness, and
a 6-character CRC32 of those, so a typo fails before any lookup. A secret
scanner finds all three with `mg[osl]_[0-9A-Za-z]{49}`.

The loopback daemon checks an `mgo_` token against the operator file alone and
an `mgs_` token against the store alone, and refuses `mgl_` outright. A share
link's listener accepts its own `mgl_` token and nothing else, so the operator
token never crosses the LAN.

## Minting

A token is never granted more than its minter holds. That is one check,
`auth.Store.Mint`, and it applies at every door:

- The CLI mints as the operator, since the shell is the user:
  `magus config mcp connector create` mints `mcp=write`, and
  `magus config console token create [--viewer]` mints `console=write` or
  `console=read`.
- The TokenService mints with the grant of the token that called it. It also
  mints only the two console presets: a browser has no business minting an MCP
  token.
- `POST /api/v1/share` mints a share link only for a caller holding at least
  `console=read`.

Every stored token expires. `--expires never` is refused, and so is a lifetime
past 366 days, or a share link past 24 hours:
[MGS9018](../reference/codes/auth/MGS9018.md), and nothing is shortened to fit.
`magus doctor` names any token that expires within 14 days, and an expired
token's file is deleted the next time the store is listed or minted into.

## Console links

A link magus prints for you to open in a browser carries a console token that
expires in 12 hours, never the operator token:

```text
open "http://127.0.0.1:7391/console/#token=$(magus config console token create --expires 12h)"
```

The browser then holds a token that cannot reach token management, so a script
injected into the console cannot mint anything.

## Agents

The guard denies `magus config token print` and `generate` to an agent session
(rule [operator-token](../reference/rules/operator-token.md)): an agent holds its
own connector token. On Linux, landlock keeps a target from reading the operator
file; elsewhere nothing does, and `magus doctor` says so.

## What the trail records

Every record made under a daemon request carries the credential that request
presented: its class, its id (the first 8 hex of its SHA-256), the name it was
minted under, and its grant at the time. Never the secret. The id is the
identity: revoke `laptop` and mint a new `laptop`, and records made under each
name a different id. An activity filter matches the class, the id or the name.

A mint or revoke through the TokenService records the token it acted on:
`token 3fa9c1d2 (laptop) console=write until 2026-12-22`.

## Upgrading from an older magus

There is no migration. An operator file that is not `mgo_` is refused with
[MGS9016](../reference/codes/auth/MGS9016.md); re-issue it with
`magus config token generate --force`. Any file left in the retired
`connectors.d` (or `connectors.json`) makes the token store fail with
[MGS9017](../reference/codes/auth/MGS9017.md), naming each file; remove them and
mint replacements. The operator token keeps working meanwhile, since it never
opens the store.
