---
title: Tokens and grants
description: How the magus server decides who may use which route. Four token classes told apart by prefix, a grant of none, read or write per surface, a need declared by every procedure, one rule for minting, the trust model, and what the activity trail records about each.
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

Every server route except the health probes and the console's app shell needs a
bearer token. This page is the model behind that: what a token may do, how a
route says what it needs, and why no token can mint a wider one.

## Grants

A **grant** is one level per surface. Levels are ordered: `none < read < write`.

| Surface   | Levels            | Reaches                                   |
| --------- | ----------------- | ----------------------------------------- |
| `tokens`  | none, write       | token management (the TokenService)       |
| `mcp`     | none, write       | the `/mcp` endpoint                       |
| `console` | none, read, write | the console's read routes, and its writes |

`tokens=read` and `mcp=read` mean nothing, so a grant naming either is refused.
A grant renders as `mcp=write` or `console=read`; the one below holds everything.

| Preset    | Grant                                  | Held by                           |
| --------- | -------------------------------------- | --------------------------------- |
| operator  | `tokens=write,mcp=write,console=write` | the operator token                |
| connector | `mcp=write`                            | an MCP client                     |
| console   | `console=write`                        | a browser tab, a console link     |
| viewer    | `console=read`                         | a second screen that only looks   |
| share     | `console=read`                         | a share link, on its own listener |

## Needs

Every Connect procedure and every `/api/` route declares the level it needs on
one surface, and the server's bearer guard compares that with the presented
token's grant. It is the only place magus decides whether a token may use a
route. The server refuses to start if a procedure has no need, or a need is
none or names a level its surface lacks, and a server whose workspace failed to
load holds every route to the same needs.

| Route                                                                                                        | Needs           |
| ------------------------------------------------------------------------------------------------------------ | --------------- |
| `/mcp`                                                                                                       | `mcp=write`     |
| TokenService, every procedure                                                                                | `tokens=write`  |
| JobService `RunJob`, every MemoryService procedure                                                           | `console=write` |
| `/api/v1/diff` and its sub-routes, `/api/v1/plan`, `/api/v1/attention`, `POST /api/v1/share`                 | `console=write` |
| every other procedure: Activity, Graph, Insight, Status, Tool, Notes, Metrics, Viewer, JobService `ListJobs` | `console=read`  |
| `/api/v1/events`, `/api/v1/insight`, `/api/v1/graph`                                                         | `console=read`  |

Memory reads need `console=write` because the notes are the operator's own and
reading them is audited like an edit. The diff, plan and attention routes need it
because they serve unreviewed source, every target's name, or an action.

A request with no token gets `401` [MGS9011](../reference/codes/auth/MGS9011.md).
A token that is wrong, expired, revoked, or of a class this listener does not
accept gets `401` [MGS9001](../reference/codes/auth/MGS9001.md), which never says
which. A valid token whose grant is below the route's need gets `403`
[MGS9015](../reference/codes/auth/MGS9015.md), naming the need.

Each MCP tool call is held to `mcp=write` again, on either transport, and a call
below it answers MGS9015 as a tool error. Over HTTP that is the credential the
guard admitted to `/mcp`. `magus mcp` serves over stdio with no token: the caller
is the process the host launched as you, so every call carries the `stdio`
credential, which holds `mcp=write` and nothing past it.

A need is checked for the whole life of a request, not only at its start. While a
stream is open the guard checks its token again every few seconds, and a token
that was revoked or expired ends the stream. A share link that closes (revoked,
superseded, expired) cancels every request on it at once and cuts any that do
not stop within a few seconds.

## Classes

A token's class is its prefix, so the server knows which store can hold it
before it hashes anything:

| Prefix | Class    | Lives                                                          | Expires                                                     |
| ------ | -------- | -------------------------------------------------------------- | ----------------------------------------------------------- |
| `mgo_` | operator | `$XDG_STATE_HOME/magus/mcp_token`, 0600                        | never; rotate it with `magus config token generate --force` |
| `mgs_` | stored   | `$XDG_STATE_HOME/magus/tokens.d/<name>.json`, only its SHA-256 | 90 days by default, at most 366                             |
| `mgl_` | share    | the server's memory                                            | 15 minutes by default, at most 24 hours                     |
| `mgx_` | exchange | `tokens.d`, only its SHA-256                                   | one minute, and spent on first use                          |

Every class has one layout: the prefix, 43 base62 characters of randomness, and
a 6-character CRC32 of those, so a typo fails before any lookup. A secret
scanner finds all four with `mg[oslx]_[0-9A-Za-z]{49}`.

The loopback server checks an `mgo_` token against the operator file alone and
an `mgs_` token against the store alone, and refuses `mgl_` and `mgx_` outright:
an exchange code is never a bearer anywhere. A share link's listener accepts its
own `mgl_` token and nothing else. The operator token is refused from any peer
that is not loopback, judged by the TCP peer address, so even a server bound past
loopback with `mcp.insecure_bind` serves only stored tokens to the network.

One credential has no token and no prefix: `socket-peer`, the caller on the
server's [unix socket](../guides/integrations/server.md#two-transports). The kernel
names the uid of the process on the other end of each connection, and the server
admits only its own, with `mcp=write` and `console=write`. It never holds
`tokens=write`: a build step runs as the same user and can reach the socket, and a
token it minted would outlive the run. Any other peer gets `403`
[MGS9022](../reference/codes/auth/MGS9022.md). The trail records
its calls with class `socket-peer`, a grant, and no id or name.

The store holds every record to the rules a mint follows, at load: a record that
holds `tokens=write`, expires more than 366 days after its creation, was created
in the future, or names another file, is skipped with
[MGS9019](../reference/codes/auth/MGS9019.md). It verifies nothing and never
disables the records beside it, and `magus doctor` fails naming it.

## Minting

A token is never granted more than its minter holds. That is one check,
`auth.Store.Mint`, and it applies at every door:

- The CLI mints as the operator, since the shell is the user:
  `magus config mcp connector create` mints `mcp=write`, and
  `magus config console token create [--viewer]` mints `console=write` or
  `console=read`. No stored token holds `tokens=write`, not even one the
  operator mints ([MGS9021](../reference/codes/auth/MGS9021.md)).
- The TokenService mints with the grant of the token that called it, and mints
  console grants only: a browser has no business minting an MCP token. A revoke
  there is held to the caller's grant the same way.
- `POST /api/v1/share` mints a share link only for a caller holding at least
  `console=read`.

Every stored token expires. `--expires never` is refused, and so is a lifetime
past 366 days, or a share link past 24 hours:
[MGS9018](../reference/codes/auth/MGS9018.md), and nothing is shortened to fit.
`magus doctor` names any token that expires within 14 days, and an expired
token's file is deleted the next time the store is listed or minted into.

Both token commands list the whole store, with each token's class and grant, and
revoke by an exact id (eight hex digits) or an exact name, never a prefix. No
name may look like an id, so one never reads as the other.

## Console links

A link magus prints for you to open in a browser carries a one-time code, never a
token:

```text
open "http://127.0.0.1:7391/console/#code=$(magus config console token create --code --expires 12h)"
```

The console trades the code at `POST /api/v1/token/exchange` for a console token
living 12 hours, and scrubs it from the address bar. A code lives one minute and
works once, so the opener's argv, a shell history or a screen share holds nothing
worth taking. The browser then holds a token that cannot reach token management,
so a script injected into the console cannot mint anything. A console handed the
operator token itself trades it for a console token at once, and says so loudly
when it cannot.

## Agents

Every credential verb is denied to an agent session (rule
[credential-verb](../reference/rules/credential-verb.md)): the console and
connector token `create` and `revoke` commands, `magus graph export --open --follow`
(its link carries a code), and `magus config token print`, `generate` and
`revoke`, however the binary is spelled. The operator file and `tokens.d` are
denied too (rule [token-state](../reference/rules/token-state.md)), to an editor
write and to any shell line that names them. An agent holds the token it was
given.

## Trust model

The guard is a seatbelt, not a boundary against a process running as you. That
process can read the operator file, write `tokens.d`, or run the CLI outside any
harness, and nothing magus does changes that; there is no keychain. What magus
does is enforce every layer it can prove: a record in `tokens.d` cannot grant more
than a mint could, a token never mints one wider than itself, an open stream ends
when its token does, the operator token never leaves loopback, and every mint is
recorded. On Linux, landlock keeps a target from reading the operator file;
elsewhere nothing does, and `magus doctor` says so.

## What the trail records

Every record made under a server request carries the credential that request
presented: its class, its id (the first 8 hex of its SHA-256), the name it was
minted under, and its grant at the time. Never the secret. A `magus mcp` tool
call carries class `stdio` with its grant, and no id or name. The id is the
identity: revoke `laptop` and mint a new `laptop`, and records made under each
name a different id. An activity filter matches the class, the id or the name.

Every mint is recorded, whichever door made it: the CLI (`cli.mint`,
`cli.generate`, `link.code`), a share (`share.mint`), a link code redeemed
(`link.redeem`), and the TokenService (`CreateToken`). Each names the minted
token's class, id, name and grant, and the credential that minted it:
`token 3fa9c1d2 (laptop) console=write until 2026-12-22`. A CLI mint outside any
workspace has no trail to land in, and says so.

## Upgrading from an older magus

There is no migration. An operator file that is not `mgo_` is refused with
[MGS9016](../reference/codes/auth/MGS9016.md); re-issue it with
`magus config token generate --force`. Any file left in the retired
`connectors.d` (or `connectors.json`) makes the token store fail with
[MGS9017](../reference/codes/auth/MGS9017.md), naming each file; remove them and
mint replacements. The operator token keeps working meanwhile, since it never
opens the store.
