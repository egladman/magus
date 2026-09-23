---
title: TokenService
generated_from: reference/api/
description: TokenService lists, mints and revokes stored tokens, and lists and revokes the active share link.
tags: [api, proto, connect, grpc, tokenservice]
---

# TokenService

TokenService lists, mints and revokes stored tokens, and lists and revokes the active share link. No response ever carries a secret except CreateTokenResponse, once.

Package `magus.token.v1alpha1`, defined in `proto/magus/token/v1alpha1/token.proto`. Source: [token.proto:25](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L25). Part of the [daemon API](../../index.md).

## Methods

### ListTokens

ListTokens returns every stored token plus the active share link (if any), each described without its secret.

`POST /magus.token.v1alpha1.TokenService/ListTokens`: unary. Source: [token.proto:28](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L28).

Takes [ListTokensRequest](#listtokensrequest), returns [ListTokensResponse](#listtokensresponse).

### RevokeToken

RevokeToken removes a stored token or the share link by name or id. Revoking the share link also closes its LAN listener. The operator token is not revocable here.

`POST /magus.token.v1alpha1.TokenService/RevokeToken`: unary. Source: [token.proto:31](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L31).

Takes [RevokeTokenRequest](#revoketokenrequest), returns [TokenInfo](#tokeninfo).

### CreateToken

CreateToken mints a console or viewer token and returns its secret ONCE. CONSOLE and CONSOLE\_READ are the only scopes it mints: OPERATOR is a file this service never opens, and CONNECTOR would be an /mcp bearer minted from a browser. The expiry is required and at most 366 days out; a request beyond it is refused, never shortened.

`POST /magus.token.v1alpha1.TokenService/CreateToken`: unary. Source: [token.proto:37](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L37).

Takes [CreateTokenRequest](#createtokenrequest), returns [CreateTokenResponse](#createtokenresponse).

## Messages

### CreateTokenRequest

Source: [token.proto:83](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L83).

| Field         | Type                      | # | Description                                                                                                 |
| ------------- | ------------------------- | - | ----------------------------------------------------------------------------------------------------------- |
| `name`        | string                    | 1 | A human label, unique among stored tokens. Empty asks the daemon to derive one.                             |
| `scope`       | [TokenScope](#tokenscope) | 2 | _enum.defined_only_ Must be TOKEN\_SCOPE\_CONSOLE or TOKEN\_SCOPE\_CONSOLE\_READ; anything else is refused. |
| `expire_time` | Timestamp                 | 3 | _optional_ Required: when the token dies, in the future and at most 366 days out.                           |

Used by: [CreateToken (request)](token.md#createtoken).

### CreateTokenResponse

CreateTokenResponse keeps a wrapper where AIP-131 would return the bare resource, because the secret is NOT part of the resource: TokenInfo is secret-free so that listing tokens cannot leak one, and the plaintext exists only in this reply.

Source: [token.proto:95](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L95).

| Field    | Type                    | # | Description                                                     |
| -------- | ----------------------- | - | --------------------------------------------------------------- |
| `token`  | [TokenInfo](#tokeninfo) | 1 |                                                                 |
| `secret` | string                  | 2 | The plaintext token. Returned once and never retrievable again. |

Used by: [CreateToken (response)](token.md#createtoken).

### ListTokensRequest

Source: [token.proto:77](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L77).

No fields.

Used by: [ListTokens (request)](token.md#listtokens).

### ListTokensResponse

Source: [token.proto:79](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L79).

| Field    | Type                             | # | Description |
| -------- | -------------------------------- | - | ----------- |
| `tokens` | [repeated TokenInfo](#tokeninfo) | 1 |             |

Used by: [ListTokens (response)](token.md#listtokens).

### RevokeTokenRequest

Source: [token.proto:101](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L101).

| Field  | Type   | # | Description                                                                    |
| ------ | ------ | - | ------------------------------------------------------------------------------ |
| `name` | string | 1 | _string.min_len: 1_ The token's name, or its identifier as TokenInfo gives it. |

Used by: [RevokeToken (request)](token.md#revoketoken).

### TokenInfo

TokenInfo describes one manageable token WITHOUT its secret, minimized to what a list and revoke UI needs: the revoke handle (identifier, the 8-hex id, never the token bytes or the full hash), the scope, the grant, the expiry, and the name. A list is still an intelligence surface, so it omits the full hash, any filesystem path, and the creation time.

Source: [token.proto:63](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L63).

| Field         | Type                      | # | Description                                                               |
| ------------- | ------------------------- | - | ------------------------------------------------------------------------- |
| `name`        | string                    | 1 | the token's name, or a label for the share link                           |
| `identifier`  | string                    | 2 | the 8-hex id; the Revoke key                                              |
| `scope`       | [TokenScope](#tokenscope) | 3 |                                                                           |
| `expire_time` | Timestamp                 | 5 |                                                                           |
| `grant`       | string                    | 7 | The grant as the daemon enforces it, e.g. "console=write" or "mcp=write". |

_Reserved: 4, 6; `created`, `last_used`._

Used by: [CreateToken (response)](token.md#createtoken), [ListTokens (response)](token.md#listtokens), [RevokeToken (response)](token.md#revoketoken).

## Enums

### TokenScope

TokenScope names the preset grant a token was minted with, so a client can group and label listed tokens. It is a label over TokenInfo.grant, which is what the daemon enforces.

Source: [token.proto:42](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L42).

| Value                      | # | Description                                                                                                                                                                                             |
| -------------------------- | - | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `TOKEN_SCOPE_UNSPECIFIED`  | 0 |                                                                                                                                                                                                         |
| `TOKEN_SCOPE_OPERATOR`     | 3 | TOKEN\_SCOPE\_OPERATOR is the operator token: every surface on loopback. It is managed SOLELY by the CLI and never appears in a ListTokensResponse; it is named here so the full taxonomy has one home. |
| `TOKEN_SCOPE_CONNECTOR`    | 1 | TOKEN\_SCOPE\_CONNECTOR is mcp=write: the grant an external agent holds.                                                                                                                                |
| `TOKEN_SCOPE_SHARE_READ`   | 2 | TOKEN\_SCOPE\_SHARE\_READ is the share link: console=read, served only on the link's own LAN listener and held only in daemon memory.                                                                   |
| `TOKEN_SCOPE_CONSOLE`      | 4 | TOKEN\_SCOPE\_CONSOLE is console=write.                                                                                                                                                                 |
| `TOKEN_SCOPE_CONSOLE_READ` | 5 | TOKEN\_SCOPE\_CONSOLE\_READ is console=read: a viewer.                                                                                                                                                  |

Used by: [CreateToken (request)](token.md#createtoken), [CreateToken (response)](token.md#createtoken), [ListTokens (response)](token.md#listtokens), [RevokeToken (response)](token.md#revoketoken).

