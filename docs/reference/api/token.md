---
title: TokenService
description: "TokenService is a VIEW-AND-REVOKE surface over the daemon's connector and share tokens: List reveals only fingerprints (never the secret bytes) and Revoke removes a token by its fingerprint or name."
tags: [api, proto, connect, grpc, tokenservice]
---

# TokenService

TokenService is a VIEW-AND-REVOKE surface over the daemon's connector and share tokens: List reveals only fingerprints (never the secret bytes) and Revoke removes a token by its fingerprint or name. There is deliberately no mint RPC - minting is CLI-only, so the browser cannot create a durable credential.

Package `magus.token.v1alpha1`, defined in `proto/magus/token/v1alpha1/token.proto`. Part of the [daemon API](index.md).

## Methods

### ListTokens

ListTokens returns every connector token plus the active share token (if any), each described by a prefix-only fingerprint - never the secret bytes.

`POST /magus.token.v1alpha1.TokenService/ListTokens`: unary.

Takes `ListTokensRequest`, returns `ListTokensResponse`.

### RevokeToken

RevokeToken removes a connector token or the share token by identifier. Revoking the share token also closes its LAN listener. The cli token is not revocable here.

`POST /magus.token.v1alpha1.TokenService/RevokeToken`: unary.

Takes `RevokeTokenRequest`, returns `TokenInfo`.

### CreateToken

CreateToken mints a console or viewer token and returns its secret ONCE.  It cannot escalate, and that is a property of the MOUNT rather than of any check here: this whole service sits behind BearerGuard(VerifyCLIBearer), the operator tier and nothing else, so a console, viewer, or connector token cannot reach this method to call it at all. The only caller who can already dominates every scope it may mint.  What it may mint is narrower still, and deliberately: CONSOLE and CONSOLE\_READ only. OPERATOR is refused because that credential lives in a file this service never opens and is rotated by the CLI; CONNECTOR is refused because minting an /mcp bearer from a browser would cross the exact tier boundary this model exists to draw.

`POST /magus.token.v1alpha1.TokenService/CreateToken`: unary.

Takes `CreateTokenRequest`, returns `CreateTokenResponse`.

## Messages

### CreateTokenRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | A human label, unique among stored tokens. Empty asks the daemon to derive one. |
| `scope` | TokenScope | 2 | Must be TOKEN\_SCOPE\_CONSOLE or TOKEN\_SCOPE\_CONSOLE\_READ; anything else is refused. |
| `expire_time` | Timestamp | 3 | _optional_ Absent means the token never expires. |

### CreateTokenResponse

CreateTokenResponse keeps a wrapper where AIP-131 would return the bare resource, because the secret is NOT part of the resource: TokenInfo is deliberately secret-free so that listing tokens cannot leak one, and the plaintext exists only in this reply and is unrecoverable afterwards. Returning TokenInfo alone would drop the one value the caller needs; adding the secret TO TokenInfo would put it on every List response.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `token` | TokenInfo | 1 |  |
| `secret` | string | 2 | The plaintext token. Returned once and never retrievable again. |

### ListTokensRequest

No fields.

### ListTokensResponse

| Field | Type | # | Description |
|-------|------|---|-------------|
| `tokens` | repeated TokenInfo | 1 |  |

### RevokeTokenRequest

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | The token's resource name. TokenInfo.identifier (the display fingerprint) is accepted here too, since it identifies the same token and is what a listing gives a reader to copy. |

### TokenInfo

TokenInfo describes one manageable token WITHOUT its secret, minimized to exactly what a view+revoke UI needs. A read-only list is still an intelligence surface - names, timing, and expiries let a viewer fingerprint the deployment - so it carries ONLY: a short revoke handle (identifier, the prefix-only fingerprint, never the token bytes or the full hash), the token class (scope), the expiry, and the user-chosen name (the operator needs the name to know which token to revoke). It deliberately omits the raw secret, the full hash, any filesystem path, the creation time, and every other internal storage detail: none is needed to revoke, all would help a viewer map the infrastructure.

| Field | Type | # | Description |
|-------|------|---|-------------|
| `name` | string | 1 | connector name, or a label for the share token |
| `identifier` | string | 2 | prefix-only fingerprint; the Revoke key |
| `scope` | TokenScope | 3 |  |
| `expire_time` | Timestamp | 5 | unset means the token never expires |

## Enums

### TokenScope

TokenScope names the CLASS a token belongs to in the credential model, so a client can group and label listed tokens - and so the full taxonomy is named in one place even for the class this service never lists. A connector token is a full MCP bearer minted for an external client; a share-read token is the short-lived, read-only secret behind "share to phone"; the operator token is the built-in cli credential.

| Value | # | Description |
|-------|---|-------------|
| `TOKEN_SCOPE_UNSPECIFIED` | 0 |  |
| `TOKEN_SCOPE_OPERATOR` | 3 | TOKEN\_SCOPE\_OPERATOR is the built-in cli token: auto-seeded on first daemon start, the bootstrap "god" credential that authenticates the operator to the daemon. It is managed SOLELY by the CLI and is structurally invisible+immutable to this service - it lives in a store this handler never opens, so it can be neither listed nor revoked here and this value therefore NEVER appears in a ListTokensResponse. It exists in the enum to name the class, not because the wire ever carries it. |
| `TOKEN_SCOPE_CONNECTOR` | 1 | TOKEN\_SCOPE\_CONNECTOR reaches /mcp and nothing else: the tier an external agent holds. |
| `TOKEN_SCOPE_SHARE_READ` | 2 | TOKEN\_SCOPE\_SHARE\_READ is the short-lived secret behind "share to phone", minted by the LAN share listener rather than stored. Distinct from CONSOLE\_READ, which reaches the same routes but is a stored, named token with its own lifetime. |
| `TOKEN_SCOPE_CONSOLE` | 4 | TOKEN\_SCOPE\_CONSOLE reaches the console read and write surfaces, never /mcp. |
| `TOKEN_SCOPE_CONSOLE_READ` | 5 | TOKEN\_SCOPE\_CONSOLE\_READ is the viewer tier: the console's read surface alone. |

