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

RevokeToken removes a stored token or the share link by exact id or exact name. Revoking the share link also closes its LAN listener. The operator token is not revocable here.

`POST /magus.token.v1alpha1.TokenService/RevokeToken`: unary. Source: [token.proto:31](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L31).

Takes [RevokeTokenRequest](#revoketokenrequest), returns [TokenInfo](#tokeninfo).

### CreateToken

CreateToken mints a stored token holding grant and returns its secret ONCE. It mints console grants only (a browser has no business minting an /mcp token), never more than the caller holds, and the expiry is required and at most 366 days out; a request beyond it is refused, never shortened.

`POST /magus.token.v1alpha1.TokenService/CreateToken`: unary. Source: [token.proto:37](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L37).

Takes [CreateTokenRequest](#createtokenrequest), returns [CreateTokenResponse](#createtokenresponse).

## Messages

### CreateTokenRequest

Source: [token.proto:87](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L87).

| Field         | Type            | # | Description                                                                                                    |
| ------------- | --------------- | - | -------------------------------------------------------------------------------------------------------------- |
| `name`        | string          | 1 | A human label, unique among stored tokens, that does not look like an id. Empty asks the daemon to derive one. |
| `expire_time` | Timestamp       | 3 | _optional_ Required: when the token dies, in the future and at most 366 days out.                              |
| `grant`       | [Grant](#grant) | 4 | The grant to mint. Console levels only; within the caller's own grant.                                         |

_Reserved: 2; `scope`._

Used by: [CreateToken (request)](token.md#createtoken).

### CreateTokenResponse

CreateTokenResponse keeps a wrapper where AIP-131 would return the bare resource, because the secret is NOT part of the resource: TokenInfo is secret-free so that listing tokens cannot leak one, and the plaintext exists only in this reply.

Source: [token.proto:104](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L104).

| Field    | Type                    | # | Description                                                     |
| -------- | ----------------------- | - | --------------------------------------------------------------- |
| `token`  | [TokenInfo](#tokeninfo) | 1 |                                                                 |
| `secret` | string                  | 2 | The plaintext token. Returned once and never retrievable again. |

Used by: [CreateToken (response)](token.md#createtoken).

### Grant

Grant is what a token may do: one level per surface, as the daemon enforces it.

Source: [token.proto:49](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L49).

| Field     | Type            | # | Description |
| --------- | --------------- | - | ----------- |
| `tokens`  | [Level](#level) | 1 |             |
| `mcp`     | [Level](#level) | 2 |             |
| `console` | [Level](#level) | 3 |             |

Used by: [CreateToken (request)](token.md#createtoken), [CreateToken (response)](token.md#createtoken), [ListTokens (response)](token.md#listtokens), [RevokeToken (response)](token.md#revoketoken).

### ListTokensRequest

Source: [token.proto:81](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L81).

No fields.

Used by: [ListTokens (request)](token.md#listtokens).

### ListTokensResponse

Source: [token.proto:83](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L83).

| Field    | Type                             | # | Description |
| -------- | -------------------------------- | - | ----------- |
| `tokens` | [repeated TokenInfo](#tokeninfo) | 1 |             |

Used by: [ListTokens (response)](token.md#listtokens).

### RevokeTokenRequest

Source: [token.proto:110](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L110).

| Field  | Type   | # | Description                                                                 |
| ------ | ------ | - | --------------------------------------------------------------------------- |
| `name` | string | 1 | _string.min_len: 1_ The token's exact id (8 hex digits), or its exact name. |

Used by: [RevokeToken (request)](token.md#revoketoken).

### TokenInfo

TokenInfo describes one manageable token WITHOUT its secret, minimized to what a list and revoke UI needs: the revoke handle (id, the 8-hex id, never the token bytes or the full hash), the class, the grant, the expiry, and the name. A list is still an intelligence surface, so it omits the full hash, any filesystem path, and the creation time.

Source: [token.proto:68](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L68).

| Field         | Type                                | # | Description                                     |
| ------------- | ----------------------------------- | - | ----------------------------------------------- |
| `name`        | string                              | 1 | the token's name, or a label for the share link |
| `id`          | string                              | 8 | the 8-hex id; a Revoke key                      |
| `class`       | [CredentialClass](#credentialclass) | 9 |                                                 |
| `grant`       | [Grant](#grant)                     | 7 |                                                 |
| `expire_time` | Timestamp                           | 5 |                                                 |

_Reserved: 2, 3, 4, 6; `identifier`, `scope`, `created`, `last_used`._

Used by: [CreateToken (response)](token.md#createtoken), [ListTokens (response)](token.md#listtokens), [RevokeToken (response)](token.md#revoketoken).

## Enums

### CredentialClass

CredentialClass is which kind of token a record is, carried in the token's prefix.

Source: [token.proto:56](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L56).

| Value                          | # | Description                           |
| ------------------------------ | - | ------------------------------------- |
| `CREDENTIAL_CLASS_UNSPECIFIED` | 0 |                                       |
| `CREDENTIAL_CLASS_OPERATOR`    | 1 | mgo\_; never listed here              |
| `CREDENTIAL_CLASS_STORED`      | 2 | mgs\_                                 |
| `CREDENTIAL_CLASS_SHARE`       | 3 | mgl\_                                 |
| `CREDENTIAL_CLASS_EXCHANGE`    | 4 | mgx\_, a console link's one-time code |

Used by: [CreateToken (response)](token.md#createtoken), [ListTokens (response)](token.md#listtokens), [RevokeToken (response)](token.md#revoketoken).

### Level

Level is how much of one surface a grant reaches. Levels are ordered: a higher level includes every lower one. The zero value is none.

Source: [token.proto:42](https://github.com/egladman/magus/blob/main/proto/magus/token/v1alpha1/token.proto#L42).

| Value               | # | Description |
| ------------------- | - | ----------- |
| `LEVEL_UNSPECIFIED` | 0 | none        |
| `LEVEL_READ`        | 1 |             |
| `LEVEL_WRITE`       | 2 |             |

Used by: [CreateToken (request)](token.md#createtoken), [CreateToken (response)](token.md#createtoken), [ListTokens (response)](token.md#listtokens), [RevokeToken (response)](token.md#revoketoken).

