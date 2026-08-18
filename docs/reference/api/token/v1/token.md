---
title: TokenService
generated_from: reference/api/
description: "TokenService is a VIEW-AND-REVOKE surface over the daemon's connector and share tokens: List reveals only fingerprints (never the secret bytes) and Revoke removes a token by its fingerprint or name."
tags: [api, proto, connect, grpc, tokenservice]
---

# TokenService

TokenService is a VIEW-AND-REVOKE surface over the daemon's connector and share tokens: List reveals only fingerprints (never the secret bytes) and Revoke removes a token by its fingerprint or name. There is deliberately no mint RPC - minting is CLI-only, so the browser cannot create a durable credential.

Package `magus.token.v1`, defined in `proto/magus/token/v1/token.proto`. Source: [token.proto:46](https://github.com/egladman/magus/blob/main/proto/magus/token/v1/token.proto#L46). Part of the [daemon API](../../index.md).

## Methods

### ListTokens

ListTokens returns every connector token plus the active share token (if any), each described by a prefix-only fingerprint - never the secret bytes.

`POST /magus.token.v1.TokenService/ListTokens`: unary. Source: [token.proto:49](https://github.com/egladman/magus/blob/main/proto/magus/token/v1/token.proto#L49).

Takes [ListTokensRequest](#listtokensrequest), returns [ListTokensResponse](#listtokensresponse).

### RevokeToken

RevokeToken removes a connector token or the share token by identifier. Revoking the share token also closes its LAN listener. The cli token is not revocable here.

`POST /magus.token.v1.TokenService/RevokeToken`: unary. Source: [token.proto:53](https://github.com/egladman/magus/blob/main/proto/magus/token/v1/token.proto#L53).

Takes [RevokeTokenRequest](#revoketokenrequest), returns [RevokeTokenResponse](#revoketokenresponse).

## Messages

### ListTokensRequest

Source: [token.proto:99](https://github.com/egladman/magus/blob/main/proto/magus/token/v1/token.proto#L99).

No fields.

Used by: [ListTokens (request)](token.md#listtokens).

### ListTokensResponse

Source: [token.proto:101](https://github.com/egladman/magus/blob/main/proto/magus/token/v1/token.proto#L101).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `tokens` | [repeated TokenInfo](#tokeninfo) | 1 |  |

Used by: [ListTokens (response)](token.md#listtokens).

### RevokeTokenRequest

Source: [token.proto:105](https://github.com/egladman/magus/blob/main/proto/magus/token/v1/token.proto#L105).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `identifier` | string | 1 | _string.min_len: 1_ identifier is a token name or fingerprint (as returned in TokenInfo.identifier). |

Used by: [RevokeToken (request)](token.md#revoketoken).

### RevokeTokenResponse

Source: [token.proto:110](https://github.com/egladman/magus/blob/main/proto/magus/token/v1/token.proto#L110).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `token` | [TokenInfo](#tokeninfo) | 1 |  |

Used by: [RevokeToken (response)](token.md#revoketoken).

### TokenInfo

TokenInfo describes one manageable token WITHOUT its secret, minimized to exactly what a view+revoke UI needs. A read-only list is still an intelligence surface - names, timing, and expiries let a viewer fingerprint the deployment - so it carries ONLY: a short revoke handle (identifier, the prefix-only fingerprint, never the token bytes or the full hash), the token class (scope), the expiry, and the user-chosen name (the operator needs the name to know which token to revoke). It deliberately omits the raw secret, the full hash, any filesystem path, the creation time, and every other internal storage detail: none is needed to revoke, all would help a viewer map the infrastructure.

Source: [token.proto:84](https://github.com/egladman/magus/blob/main/proto/magus/token/v1/token.proto#L84).

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | connector name, or a label for the share token |
| `identifier` | string | 2 | prefix-only fingerprint; the Revoke key |
| `scope` | [TokenScope](#tokenscope) | 3 |  |
| `expires` | Timestamp | 5 | unset means the token never expires |

_Reserved: 4, 6; `created`, `last_used`._

Used by: [ListTokens (response)](token.md#listtokens), [RevokeToken (response)](token.md#revoketoken).

## Enums

### TokenScope

TokenScope names the CLASS a token belongs to in the three-tier credential model, so a client can group and label listed tokens - and so the full taxonomy is named in one place even for the class this service never lists. A connector token is a full MCP bearer minted for an external client; a share-read token is the short-lived, read-only secret behind "share to phone"; the operator token is the built-in cli credential.

Source: [token.proto:62](https://github.com/egladman/magus/blob/main/proto/magus/token/v1/token.proto#L62).

| Value | # | Description |
|-------|---|-------------|
| `TOKEN_SCOPE_UNSPECIFIED` | 0 |  |
| `TOKEN_SCOPE_OPERATOR` | 3 | TOKEN\_SCOPE\_OPERATOR is the built-in cli token: auto-seeded on first daemon start, the bootstrap "god" credential that authenticates the operator to the daemon. It is managed SOLELY by the CLI and is structurally invisible+immutable to this service - it lives in a store this handler never opens, so it can be neither listed nor revoked here and this value therefore NEVER appears in a ListTokensResponse. It exists in the enum to name the class, not because the wire ever carries it. |
| `TOKEN_SCOPE_CONNECTOR` | 1 |  |
| `TOKEN_SCOPE_SHARE_READ` | 2 |  |

Used by: [ListTokens (response)](token.md#listtokens), [RevokeToken (response)](token.md#revoketoken).

